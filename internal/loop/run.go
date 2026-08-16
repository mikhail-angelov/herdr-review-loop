package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

// Client is everything the run needs from Herdr: driving the two agents, plus the workspace and
// pane calls that keep the panel visible and the progress token current.
type Client interface {
	AgentClient
	AgentList(context.Context) ([]herdr.Agent, error)
	WorkspaceReportMetadata(context.Context, string, string, bool) error
	NotificationShow(context.Context, string, string) error
	PluginPaneOpen(context.Context, string, string) (string, error)
	PaneLayout(context.Context, string) (herdr.PaneLayout, error)
	PaneResize(context.Context, string, string, float64) error
}

// The two phases of a round, named once so events, archive file names and progress tokens agree.
const (
	PhaseReview        = "review"
	PhaseFix           = "fix"
	PhaseBrief         = "brief"
	PhaseOneShotAction = "one-shot-action"
)

// Run is one invocation of the review loop, wired with everything it needs to execute.
type Run struct {
	Client Client
	Config config.Values
	// Profile is where the round policy in Config came from. It is provenance only — the policy
	// itself is already merged into Config — and it exists so the manifest can name the file.
	Profile     config.Profile
	Environment herdr.Environment
	Log         Log
	// Version is the plugin version, recorded in the manifest so an archived run names the build
	// that produced it.
	Version string
	// OutputWait is how long a phase waits for an agent's file after its turn ends. The agent has
	// already stopped by then, so this only covers the write landing on disk. Zero means the
	// default; it is a field because it is the one delay a test cannot afford to sit through.
	OutputWait time.Duration
	// OneShot runs one review and waits for a panel choice before a single author turn.
	OneShot bool
}

const cleanupTimeout = 2 * time.Second

const defaultOutputWait = 15 * time.Second

func (r Run) outputWait() time.Duration {
	if r.OutputWait > 0 {
		return r.OutputWait
	}
	return defaultOutputWait
}

// Execute runs review and apply rounds until the reviewer reports clean, the round budget is spent,
// or something stops the loop. It holds the run lock for its whole duration, so only one loop runs
// per state directory.
func (r Run) Execute(ctx context.Context, dryRun bool) error {
	author, reviewer, err := r.pair(ctx, dryRun)
	if err != nil {
		return Exit(ExitTool, err)
	}
	repository, err := r.Environment.Repository()
	if err != nil {
		return Exit(ExitTool, err)
	}
	if tracked := TrackedRunFiles(ctx, repository); len(tracked) > 0 {
		return Exitf(ExitTool, "git already tracks %s inside %s, so it cannot be hidden from the review — run `git rm -r --cached %s` first", strings.Join(tracked, ", "), RunSubdir, RunSubdir)
	}
	runDir, err := OpenRunDir(repository)
	if err != nil {
		return Exit(ExitTool, err)
	}
	defer func() { _ = runDir.Close() }()
	scope, err := ParseScope(r.Config.Scope)
	if err == nil {
		scope, err = scope.Resolve(runDir)
	}
	if err != nil {
		return Exit(ExitTool, err)
	}
	if dryRun {
		return r.describe(author, reviewer, runDir, scope)
	}
	if logErr := r.Log.Write(fmt.Sprintf("author %s; review by %s", herdr.Describe(author), herdr.Describe(reviewer))); logErr != nil {
		return Exit(ExitTool, logErr)
	}
	lock, err := AcquireLock(r.Environment.StateDir, author.WorkspaceID)
	if err != nil {
		return Exit(ExitTool, err)
	}
	defer func() { _ = lock.Release() }()
	if err := runDir.Prepare(); err != nil {
		return Exit(ExitTool, err)
	}
	if err := ExcludeRun(ctx, repository); err != nil {
		return Exit(ExitTool, err)
	}
	return r.execute(ctx, author, reviewer, repository, runDir, scope)
}

// session is the mutable state of one run: what has been settled, what has been raised, and how it
// ended. Keeping it in one place is what lets every exit path write the same report.
type session struct {
	scope    Scope
	record   RunRecord
	archive  *Archive
	journal  Journal
	disputes Disputes
	report   Report
	notified string
}

// execute is the run proper, once the lock is held and the run directory exists.
func (r Run) execute(ctx context.Context, author, reviewer herdr.Agent, repository string, runDir *RunDir, scope Scope) error {
	// rotation reserves a slot for the run that is about to start, so a completed run never leaves
	// one archive more than archive.keep behind
	if err := Rotate(r.Environment.StateDir, r.Config.Archive.Keep-1); err != nil {
		return Exit(ExitTool, err)
	}
	state := &session{scope: scope, record: RunRecord{
		ID:         NewRunID(time.Now()),
		Repository: repository,
		Author:     herdr.Describe(author),
		Reviewer:   herdr.Describe(reviewer),
		Started:    time.Now().UTC(),
		Outcome:    "running",
	}}
	archive, err := OpenArchive(r.Environment.StateDir, state.record.ID, r.Config.Archive.RawOutput)
	if err != nil {
		return Exit(ExitTool, err)
	}
	state.archive = archive
	state.report.Run = state.record.ID
	if err := r.Log.WriteRun(state.record); err != nil {
		return Exit(ExitTool, err)
	}
	checkpoints := r.checkpoints(ctx, repository, state.record.ID)
	if err := archive.WriteManifest(r.manifest(ctx, state, author, reviewer, checkpoints)); err != nil {
		return Exit(ExitTool, err)
	}
	defer r.close(state, author.WorkspaceID)
	r.ensurePanel(ctx, author)
	if r.OneShot {
		return r.oneShot(ctx, state, author, reviewer, runDir, checkpoints)
	}
	return r.rounds(ctx, state, author, reviewer, runDir, checkpoints)
}

// rounds is the loop itself. Every exit from it names an outcome, which is what the report, the
// notification and the process exit code are all derived from.
func (r Run) rounds(ctx context.Context, state *session, author, reviewer herdr.Agent, runDir *RunDir, checkpoints Checkpoints) error {
	for round := 1; round <= r.Config.MaxIterations; round++ {
		state.record.Rounds = round
		state.report.Rounds = round
		policy := r.policy(state, runDir, round)
		r.reportProgress(ctx, author.WorkspaceID, PhaseReview, round)
		review, err := r.reviewPhase(ctx, state, reviewer, runDir.Round(round), policy)
		if err != nil {
			return r.fail(state, err)
		}
		last := round == r.Config.MaxIterations
		switch review.Resolve() {
		case Clean:
			// a clean round can still have produced findings the bar withheld, and nothing a run
			// produced is lost while the run is kept
			r.record(state, round, Review{Filtered: review.Filtered, PreExisting: review.PreExisting}, Decisions{}, "", "")
			return r.finishClean(ctx, state, author, round, len(review.Filtered))
		case Blocked:
			r.record(state, round, review, Decisions{}, ActionUnreviewed, "the reviewer asked a question")
			state.report.OpenQuestions = review.OpenQuestions
			_ = state.archive.Event(round, PhaseReview, EventBlocked, questionSummary(review.OpenQuestions))
			return r.fail(state, Exitf(ExitBlocked, "the reviewer needs a human: %s", questionSummary(review.OpenQuestions)))
		case Dirty, Contradictory:
		}
		_ = r.Log.Write(fmt.Sprintf("round %d: %d finding(s)", round, len(review.Findings)))
		if last {
			r.record(state, round, review, Decisions{}, ActionUnreviewed, "the round budget was spent")
			break
		}
		r.reportProgress(ctx, author.WorkspaceID, PhaseFix, round)
		decisions, err := r.fixPhase(ctx, state, author, runDir.Round(round), policy, review, checkpoints)
		if err != nil {
			r.record(state, round, review, Decisions{}, ActionUnreviewed, "the author phase failed")
			return r.fail(state, err)
		}
		r.record(state, round, review, decisions, "", "")
		state.journal.Record(round, review, decisions)
		state.disputes.Record(round, review, decisions)
		checkpoints.SaveQuietly(ctx, round)
		if disputed, stuck := state.disputes.Stuck(); stuck {
			_ = state.archive.Event(round, PhaseFix, EventDegraded, "stuck: "+disputed)
			return r.fail(state, Exitf(ExitFindings, "stuck after %d round(s): the author keeps applying a fix the reviewer keeps raising — %s", round, disputed))
		}
	}
	return r.fail(state, Exitf(ExitFindings, "stopped after %d round(s) with findings still open", state.record.Rounds))
}

// baselinePatch is the file a regressions-only round is judged against, written into the run
// directory rather than handed over from the archive: the archive is outside the repository, and a
// sandboxed agent may not read there.
const baselinePatch = "baseline.patch"

// policy resolves the round policy for one round and materializes whatever the round needs to be
// carried out. regressions_only has no baseline on round 1 and is ignored with a warning there,
// which is the only sensible reading of "regressions only" before anything has been changed.
func (r Run) policy(state *session, runDir *RunDir, round int) RoundPolicy {
	policy := Policy(r.Config.Rounds, round)
	if !policy.RegressionsOnly {
		return policy
	}
	if round == 1 {
		_ = r.Log.Write("round 1 has no earlier fixes to judge against; ignoring regressions_only")
		return policy
	}
	baseline := state.archive.Patches(round)
	if strings.TrimSpace(baseline) == "" {
		_ = r.Log.Write(fmt.Sprintf("round %d recorded no earlier changes; ignoring regressions_only", round))
		return policy
	}
	path, err := runDir.Publish(baselinePatch, baseline)
	if err != nil {
		_ = r.Log.Write("could not publish the regression baseline: " + err.Error())
		return policy
	}
	policy.Baseline = path
	return policy
}

// oneShot runs one review pass, shows it through the author, then waits for one panel choice.
// It never starts another review round, whatever the author decides to do with the findings.
func (r Run) oneShot(ctx context.Context, state *session, author, reviewer herdr.Agent, runDir *RunDir, checkpoints Checkpoints) error {
	const round = 1
	state.record.Rounds, state.report.Rounds = round, round
	policy := r.policy(state, runDir, round)
	r.reportProgress(ctx, author.WorkspaceID, PhaseReview, round)
	review, err := r.reviewPhase(ctx, state, reviewer, runDir.Round(round), policy)
	if err != nil {
		return r.fail(state, err)
	}
	if review.Resolve() == Blocked {
		r.record(state, round, review, Decisions{}, ActionUnreviewed, "the reviewer asked a question")
		state.report.OpenQuestions = review.OpenQuestions
		return r.fail(state, Exitf(ExitBlocked, "the reviewer needs a human: %s", questionSummary(review.OpenQuestions)))
	}
	if briefErr := r.oneShotBrief(ctx, state, author, runDir.Round(round), round); briefErr != nil {
		r.record(state, round, review, Decisions{}, ActionUnreviewed, "the author could not present the review")
		return r.fail(state, briefErr)
	}
	pending := OneShotPending{Workspace: author.WorkspaceID, Repository: state.record.Repository, Run: state.record.ID, Round: round}
	if pendingErr := CreateOneShotPending(r.Environment.StateDir, pending); pendingErr != nil {
		return r.fail(state, Exit(ExitTool, pendingErr))
	}
	defer clearOneShot(r.Environment.StateDir)
	_ = state.archive.Event(round, PhaseBrief, EventPhaseDone, "awaiting panel choice")
	choice, err := waitOneShotChoice(ctx, r.Environment.StateDir, author.WorkspaceID, state.record.Repository)
	if err != nil {
		r.record(state, round, review, Decisions{}, ActionUnreviewed, "no panel choice was made")
		return r.fail(state, r.classify(state, round, PhaseBrief, err))
	}
	r.reportProgress(ctx, author.WorkspaceID, PhaseOneShotAction, round)
	if len(review.Findings) == 0 {
		if actionErr := r.oneShotAction(ctx, state, author, round, choice); actionErr != nil {
			return r.fail(state, actionErr)
		}
		r.record(state, round, review, Decisions{}, "", "")
		return r.finishClean(ctx, state, author, round, len(review.Filtered))
	}
	decisions, err := r.fixPhaseWithInstruction(ctx, state, author, runDir.Round(round), policy, review, checkpoints, oneShotInstruction(choice))
	if err != nil {
		r.record(state, round, review, Decisions{}, ActionUnreviewed, "the one-shot author phase failed")
		return r.fail(state, err)
	}
	r.record(state, round, review, decisions, "", "")
	checkpoints.SaveQuietly(ctx, round)
	return r.fail(state, Exitf(ExitFindings, "one-shot review completed; no follow-up review was run"))
}

func (r Run) oneShotBrief(ctx context.Context, state *session, author herdr.Agent, round *Round, number int) error {
	if err := r.Client.AgentFocus(ctx, herdr.Target(author)); err != nil {
		return err
	}
	phase, cancel := context.WithTimeout(ctx, r.Config.Timeouts.Fix)
	defer cancel()
	if err := r.prepareAgent(phase, author); err != nil {
		return err
	}
	prompt := OneShotBriefPrompt(round.ReviewPath())
	if err := state.archive.Prompt(number, PhaseBrief, prompt); err != nil {
		return err
	}
	return r.submit(phase, state, author, prompt, number, PhaseBrief)
}

func (r Run) oneShotAction(ctx context.Context, state *session, author herdr.Agent, number int, choice OneShotChoice) error {
	phase, cancel := context.WithTimeout(ctx, r.Config.Timeouts.Fix)
	defer cancel()
	if err := r.prepareAgent(phase, author); err != nil {
		return err
	}
	prompt := OneShotActionPrompt(choice)
	if err := state.archive.Prompt(number, PhaseOneShotAction, prompt); err != nil {
		return err
	}
	return r.submit(phase, state, author, prompt, number, PhaseOneShotAction)
}

func oneShotInstruction(choice OneShotChoice) string {
	switch choice.Action {
	case OneShotApply:
		return "The user chose Apply. Apply the findings you agree with, then record a decision for every id."
	case OneShotCancel:
		return "The user chose Cancel. Do not edit code. Record every finding as deferred with a note that the user canceled this one-shot review."
	default:
		return "The user supplied this instruction:\n" + choice.Text
	}
}

// reviewPhase runs the degradation ladder: a review turn, one reformat turn when its output cannot
// be read, then a session reset and another attempt for each remaining retry. The loop never enters
// an author phase without structured findings, because an author given prose has no ids to decide.
func (r Run) reviewPhase(ctx context.Context, state *session, reviewer herdr.Agent, round *Round, policy RoundPolicy) (Review, error) {
	number := policy.Number
	_ = state.archive.Event(number, PhaseReview, EventPhaseStart, "")
	_ = r.Log.Write(fmt.Sprintf("--- round %d/%d: review", number, r.Config.MaxIterations))
	var failure error
	for attempt := 0; attempt <= r.Config.Retries; attempt++ {
		if attempt > 0 {
			_ = state.archive.Event(number, PhaseReview, EventRetry, failure.Error())
			_ = r.Log.Write(fmt.Sprintf("review retry %d/%d: %s", attempt, r.Config.Retries, failure))
		}
		review, err := r.reviewAttempt(ctx, state, reviewer, round, policy, attempt)
		if err == nil {
			_ = state.archive.Event(number, PhaseReview, EventPhaseDone, fmt.Sprintf("%d finding(s)", len(review.Findings)))
			return review, nil
		}
		if terminal(err) {
			return Review{}, r.classify(state, number, PhaseReview, err)
		}
		recordAttemptFailure(state, number, PhaseReview, err)
		failure = err
	}
	_ = state.archive.Event(number, PhaseReview, EventDegraded, "parse: "+failure.Error())
	return Review{}, Exitf(ExitAgent, "the reviewer's output could not be read after %d attempt(s): %w", r.Config.Retries+1, failure)
}

// reviewAttempt is one pass at getting a readable review. The round directory is created here and
// nowhere else, which is what makes the presence of review.json proof that this round's reviewer
// wrote it — no timestamp comparison is involved.
func (r Run) reviewAttempt(ctx context.Context, state *session, reviewer herdr.Agent, round *Round, policy RoundPolicy, attempt int) (Review, error) {
	number := policy.Number
	if err := r.Client.AgentFocus(ctx, herdr.Target(reviewer)); err != nil {
		return Review{}, err
	}
	phase, cancel := context.WithTimeout(ctx, r.Config.Timeouts.Review)
	defer cancel()
	if err := r.prepareAgent(phase, reviewer); err != nil {
		return Review{}, err
	}
	if err := r.freshRound(round, attempt); err != nil {
		return Review{}, err
	}
	if err := r.requestReview(phase, state, reviewer, round, policy, attempt); err != nil {
		return Review{}, err
	}
	review, raw, err := r.readReview(phase, state, round, number)
	if err == nil {
		if archiveErr := r.archiveRaw(state, number, PhaseReview, attempt, false, raw); archiveErr != nil {
			return Review{}, archiveErr
		}
		return r.acceptReview(state, round, number, review)
	}
	if archiveErr := r.archiveRaw(state, number, PhaseReview, attempt, false, raw); archiveErr != nil {
		return Review{}, archiveErr
	}
	// step 3: one reformat turn, to an agent whose context still holds the review it just produced
	_ = state.archive.Event(number, PhaseReview, EventParseFallback, err.Error())
	_ = r.Log.Write("review unreadable, asking for a reformat: " + err.Error())
	reformat := ReviewReformatPrompt(round.ReviewPath(), err.Error())
	if promptErr := state.archive.PromptAttempt(number, PhaseReview, attempt, true, reformat); promptErr != nil {
		return Review{}, promptErr
	}
	if promptErr := r.submit(phase, state, reviewer, reformat, number, PhaseReview); promptErr != nil {
		return Review{}, promptErr
	}
	review, raw, err = r.readReview(phase, state, round, number)
	if archiveErr := r.archiveRaw(state, number, PhaseReview, attempt, true, raw); archiveErr != nil {
		return Review{}, archiveErr
	}
	if err != nil {
		return Review{}, err
	}
	return r.acceptReview(state, round, number, review)
}

// requestReview asks for this round's findings. A kind with a review command of its own runs that
// command and is then asked to record what it found; a kind without one gets the built-in prompt,
// which is the only place a review taxonomy of ours remains. Every turn either way is archived, so
// the request is legible afterwards whichever path it took.
func (r Run) requestReview(ctx context.Context, state *session, reviewer herdr.Agent, round *Round, policy RoundPolicy, attempt int) error {
	number := policy.Number
	native, delegated := Adapt(reviewer.Kind, r.Config.ReviewCommand, policy, state.scope, round.ReviewPath(), state.journal.Entries())
	if !delegated {
		prompt := ReviewPrompt(state.scope, policy, r.Config.MaxIterations, round.ReviewPath(), state.journal.Entries())
		if err := state.archive.PromptAttempt(number, PhaseReview, attempt, false, prompt); err != nil {
			return err
		}
		return r.submit(ctx, state, reviewer, prompt, number, PhaseReview)
	}
	turns := []string{native.Preamble, native.Command, native.Capture}
	if err := state.archive.PromptAttempt(number, PhaseReview, attempt, false, strings.Join(nonEmpty(turns), "\n\n---\n\n")); err != nil {
		return err
	}
	for _, turn := range nonEmpty(turns) {
		if err := r.submit(ctx, state, reviewer, turn, number, PhaseReview); err != nil {
			return err
		}
	}
	return nil
}

func nonEmpty(values []string) []string {
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// acceptReview assigns the ids and fingerprints the author decides on, writes them back where the
// author reads them, and archives both what came in and what the loop made of it.
func (r Run) acceptReview(state *session, round *Round, number int, review Review) (Review, error) {
	review.Identify(number)
	// the bar is applied after the ids, so a filtered finding keeps the id it was raised under and
	// the report can account for it in the reviewer's own order
	review = review.Filter(r.Config.MinVerdict)
	if len(review.Filtered) != 0 {
		_ = state.archive.Event(number, PhaseReview, EventFiltered, fmt.Sprintf("%d below %s", len(review.Filtered), r.Config.MinVerdict))
		_ = r.Log.Write(fmt.Sprintf("round %d: %d finding(s) filtered below %s", number, len(review.Filtered), r.Config.MinVerdict))
	}
	if err := state.archive.Parsed(number, reviewFile, review); err != nil {
		return Review{}, err
	}
	encoded, err := marshalIndent(review.AuthorView())
	if err != nil {
		return Review{}, err
	}
	if err := round.WriteReview(encoded); err != nil {
		return Review{}, err
	}
	return review, nil
}

// submit sends one prompt under the stall watchdog. A silent agent costs timeouts.stall and a
// retry rather than the phase's whole budget, and the stall is named as such in the event stream
// so a later diagnosis does not have to tell it apart from a phase that simply ran long.
func (r Run) submit(ctx context.Context, state *session, agent herdr.Agent, prompt string, round int, phase string) error {
	stalled, err := WatchStall(ctx, r.Client, agent, r.Config.Timeouts.Stall, func(watched context.Context) error {
		_, submitErr := SubmitAndWait(watched, r.Client, agent, prompt)
		return submitErr
	})
	// a phase that ended because the run itself was canceled or ran out of budget is that, not a
	// stall, even when the watchdog happened to fire on the same tick
	if !stalled || ctx.Err() != nil {
		return err
	}
	detail := fmt.Sprintf("no output from %s for %s", herdr.Describe(agent), r.Config.Timeouts.Stall)
	_ = state.archive.Event(round, phase, EventStall, detail)
	_ = r.Log.Write(detail)
	return errors.New(detail)
}

func (r Run) archiveRaw(state *session, round int, phase string, attempt int, reformat bool, raw string) error {
	if raw == "" {
		return nil
	}
	return state.archive.RawAttempt(round, phase, attempt, reformat, raw)
}

// readReview polls for output the loop can read. A file that exists but does not parse counts as
// not yet written while the wait lasts — that is a partial write; once the wait is over it is a
// parse failure and enters the ladder.
func (r Run) readReview(ctx context.Context, state *session, round *Round, number int) (Review, string, error) {
	raw, err := waitForOutput(ctx, r.outputWait(), round.ReadReview, func(contents string) error {
		_, _, err := ParseReview(contents)
		return err
	})
	if err != nil {
		return Review{}, raw, err
	}
	review, notes, err := ParseReview(raw)
	if err != nil {
		return Review{}, raw, err
	}
	if review.Resolve() == Contradictory {
		return Review{}, raw, errors.New("status says findings but the findings array is empty")
	}
	r.recordNotes(state, number, PhaseReview, notes)
	return review, raw, nil
}

// recordNotes carries what a parse had to drop or fall back to. A review that only parsed as
// markdown is a degraded round, and a note in the global log alone would leave events.jsonl — and
// so the archive and the panel — unable to tell it from a round that answered in JSON.
func (r Run) recordNotes(state *session, number int, phase string, notes []string) {
	for _, note := range notes {
		_ = r.Log.Write(note)
		if strings.HasPrefix(note, NotePrefixParseFallback) {
			_ = state.archive.Event(number, phase, EventParseFallback, note)
		}
	}
}

// fixPhase has the author act on the findings and decide every one of them, with the same ladder
// the reviewer's output goes through. An exhausted budget stops the run, because the next round
// would review unchanged code.
func (r Run) fixPhase(ctx context.Context, state *session, author herdr.Agent, round *Round, policy RoundPolicy, review Review, checkpoints Checkpoints) (Decisions, error) {
	return r.fixPhaseWithInstruction(ctx, state, author, round, policy, review, checkpoints, "")
}

func (r Run) fixPhaseWithInstruction(ctx context.Context, state *session, author herdr.Agent, round *Round, policy RoundPolicy, review Review, checkpoints Checkpoints, instruction string) (Decisions, error) {
	number := policy.Number
	_ = state.archive.Event(number, PhaseFix, EventPhaseStart, "")
	_ = r.Log.Write(fmt.Sprintf("--- round %d/%d: apply", number, r.Config.MaxIterations))
	before := ""
	if checkpoints.Enabled {
		before, _ = checkpoints.Tree(ctx)
	}
	var failure error
	for attempt := 0; attempt <= r.Config.Retries; attempt++ {
		if attempt > 0 {
			_ = state.archive.Event(number, PhaseFix, EventRetry, failure.Error())
			_ = r.Log.Write(fmt.Sprintf("apply retry %d/%d: %s", attempt, r.Config.Retries, failure))
		}
		decisions, err := r.fixAttempt(ctx, state, author, round, policy, attempt, review, instruction)
		if err == nil {
			r.savePatch(ctx, state, checkpoints, number, before)
			_ = state.archive.Event(number, PhaseFix, EventPhaseDone, decisionSummary(decisions))
			return decisions, nil
		}
		if terminal(err) {
			return Decisions{}, r.classify(state, number, PhaseFix, err)
		}
		recordAttemptFailure(state, number, PhaseFix, err)
		failure = err
	}
	_ = state.archive.Event(number, PhaseFix, EventDegraded, "decisions: "+failure.Error())
	return Decisions{}, Exitf(ExitAgent, "the author recorded nothing usable after %d attempt(s): %w", r.Config.Retries+1, failure)
}

// fixAttempt is one pass at getting a complete decision record. The round directory already exists
// — the review phase made it — so what has to be absent here is decisions.json, not the directory.
func (r Run) fixAttempt(ctx context.Context, state *session, author herdr.Agent, round *Round, policy RoundPolicy, attempt int, review Review, instruction string) (Decisions, error) {
	number := policy.Number
	if err := r.Client.AgentFocus(ctx, herdr.Target(author)); err != nil {
		return Decisions{}, err
	}
	phase, cancel := context.WithTimeout(ctx, r.Config.Timeouts.Fix)
	defer cancel()
	if err := r.prepareAgent(phase, author); err != nil {
		return Decisions{}, err
	}
	if err := round.RemoveDecisions(); err != nil {
		return Decisions{}, err
	}
	ids := review.IDs()
	prompt := FixPromptWithInstruction(state.scope, policy, round.ReviewPath(), round.DecisionsPath(), ids, instruction)
	if err := state.archive.PromptAttempt(number, PhaseFix, attempt, false, prompt); err != nil {
		return Decisions{}, err
	}
	if err := r.submit(phase, state, author, prompt, number, PhaseFix); err != nil {
		return Decisions{}, err
	}
	decisions, raw, err := r.readDecisions(phase, round, ids)
	if archiveErr := r.archiveRaw(state, number, PhaseFix, attempt, false, raw); archiveErr != nil {
		return Decisions{}, archiveErr
	}
	if err != nil {
		_ = state.archive.Event(number, PhaseFix, EventParseFallback, err.Error())
		_ = r.Log.Write("decisions unreadable, asking for a reformat: " + err.Error())
		reformat := FixReformatPrompt(round.DecisionsPath(), err.Error(), ids)
		if promptErr := state.archive.PromptAttempt(number, PhaseFix, attempt, true, reformat); promptErr != nil {
			return Decisions{}, promptErr
		}
		if promptErr := r.submit(phase, state, author, reformat, number, PhaseFix); promptErr != nil {
			return Decisions{}, promptErr
		}
		decisions, raw, err = r.readDecisions(phase, round, ids)
		if archiveErr := r.archiveRaw(state, number, PhaseFix, attempt, true, raw); archiveErr != nil {
			return Decisions{}, archiveErr
		}
	}
	if err != nil {
		return Decisions{}, err
	}
	if err := state.archive.Parsed(number, decisionsFile, decisions); err != nil {
		return Decisions{}, err
	}
	return decisions, nil
}

// readDecisions requires a record that decides something. A round that changed nothing and
// recorded nothing would otherwise repeat until the budget was gone.
func (r Run) readDecisions(ctx context.Context, round *Round, ids []string) (Decisions, string, error) {
	raw, err := waitForOutput(ctx, r.outputWait(), round.ReadDecisions, func(contents string) error {
		decisions, _, err := ParseDecisions(contents, ids)
		if err == nil && decisions.Decided() == 0 {
			return errors.New("no id was decided")
		}
		return err
	})
	if err != nil {
		return Decisions{}, raw, err
	}
	decisions, notes, err := ParseDecisions(raw, ids)
	if err != nil {
		return Decisions{}, raw, err
	}
	if decisions.Decided() == 0 {
		return Decisions{}, raw, errors.New("every finding was left undecided")
	}
	for _, note := range notes {
		_ = r.Log.Write(note)
	}
	return decisions, raw, nil
}

// waitForOutput polls for an agent's file and returns it once accept is satisfied. The last
// contents seen are returned even on failure, so the archive keeps what could not be read.
func waitForOutput(ctx context.Context, wait time.Duration, read func() (string, bool, error), accept func(string) error) (string, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(max(min(wait/4, 500*time.Millisecond), time.Millisecond))
	defer tick.Stop()
	last, reason := "", errors.New("the file was never written")
	for {
		contents, found, err := read()
		if err != nil {
			return last, err
		}
		if found {
			last = contents
			if reason = accept(contents); reason == nil {
				return contents, nil
			}
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("phase ended: %w", ctx.Err())
		case <-deadline.C:
			return last, reason
		case <-tick.C:
		}
	}
}

// freshRound gives an attempt a directory that did not exist before it: the first attempt creates
// it, a retry replaces the failed attempt's one.
func (r Run) freshRound(round *Round, attempt int) error {
	if attempt == 0 {
		return round.Create()
	}
	return round.Reset()
}

// prepareAgent settles an agent, clears its session and settles it again, so each round is judged
// on the code rather than on what the agent remembers saying.
func (r Run) prepareAgent(ctx context.Context, agent herdr.Agent) error {
	if _, err := Settle(ctx, r.Client, agent); err != nil {
		return err
	}
	if err := ResetSession(ctx, r.Client, agent, r.Config.ResetCommand, r.note); err != nil {
		return err
	}
	_, err := Settle(ctx, r.Client, agent)
	return err
}

// savePatch records what the author phase changed, new files included. It answers the one question
// nothing else answers: is the loop converging, or moving the same lines back and forth.
func (r Run) savePatch(ctx context.Context, state *session, checkpoints Checkpoints, round int, before string) {
	if !checkpoints.Enabled || before == "" {
		return
	}
	after, err := checkpoints.Tree(ctx)
	if err != nil {
		_ = r.Log.Write("round patch failed: " + err.Error())
		return
	}
	patch, err := checkpoints.Diff(ctx, before, after)
	if err != nil {
		_ = r.Log.Write("round patch failed: " + err.Error())
		return
	}
	if err := state.archive.Patch(round, patch); err != nil {
		_ = r.Log.Write("round patch failed: " + err.Error())
	}
}

// terminal reports whether a failure is one no retry can help with: a canceled run, or an agent
// waiting on a human. Everything else is worth another attempt.
func terminal(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, ErrBlocked)
}

// classify turns a terminal failure into the exit code that says what a human has to do about it.
func (r Run) classify(state *session, round int, phase string, err error) error {
	if errors.Is(err, context.Canceled) {
		_ = state.archive.Event(round, phase, EventCanceled, "")
		return Exit(ExitCanceled, err)
	}
	_ = state.archive.Event(round, phase, EventBlocked, err.Error())
	return Exit(ExitBlocked, err)
}

// recordAttemptFailure names what went wrong in the event stream. A phase that ran out of its own
// budget is a timeout rather than a bad answer, and the archive is where that distinction has to
// survive: the exit code only separates "the loop broke" from "the code is not clean".
func recordAttemptFailure(state *session, round int, phase string, err error) {
	if errors.Is(err, context.DeadlineExceeded) {
		_ = state.archive.Event(round, phase, EventTimeout, err.Error())
	}
}

// record adds every finding of a round to the report, with the action taken on it. Pre-existing
// findings are recorded too: nothing a run produced is lost while the run is kept.
func (r Run) record(state *session, round int, review Review, decisions Decisions, override, reason string) {
	for _, finding := range review.Findings {
		action, note := override, reason
		if action == "" {
			action, note = ActionMissing, ""
			if decision, found := decisions.Find(finding.ID); found {
				action, note = decision.Action, decision.Note
			}
		}
		state.report.Findings = append(state.report.Findings, reportEntry(finding, round, action, note))
	}
	for _, finding := range review.Filtered {
		state.report.Findings = append(state.report.Findings, reportEntry(finding, round, ActionFiltered, finding.Verdict))
	}
	for _, finding := range review.PreExisting {
		state.report.Findings = append(state.report.Findings, reportEntry(finding, round, ActionPreExisting, ""))
	}
}

func reportEntry(finding Finding, round int, action, note string) ReportFinding {
	return ReportFinding{
		ID:          finding.ID,
		Fingerprint: finding.Fingerprint,
		Round:       round,
		File:        finding.File,
		Line:        finding.Line,
		Category:    finding.Category,
		Severity:    finding.Severity,
		Verdict:     finding.Verdict,
		Title:       finding.Title,
		Action:      action,
		Note:        note,
	}
}

// finishClean is the only path that ends a run with code 0.
func (r Run) finishClean(ctx context.Context, state *session, author herdr.Agent, round, filtered int) error {
	_ = r.Client.AgentFocus(ctx, herdr.Target(author))
	outcome := fmt.Sprintf("clean after %d round(s)", round)
	if filtered > 0 {
		outcome = fmt.Sprintf("%s, %d filtered", outcome, filtered)
	}
	_ = r.Log.Write(outcome)
	state.notified = outcome
	state.record.Outcome = "clean"
	state.report.Outcome = outcome
	state.report.ExitCode = ExitClean
	return nil
}

// fail records how a run ended before returning the error that ends it.
func (r Run) fail(state *session, err error) error {
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "canceled"
	}
	_ = r.Log.Write(message)
	state.notified = message
	state.record.Outcome = message
	state.report.Outcome = message
	state.report.ExitCode = ExitCode(err)
	return err
}

// close publishes the run record and the report, clears the progress token and notifies. It runs
// on every exit path, which is what makes the report the complete account of a run rather than the
// account of the runs that went well.
func (r Run) close(state *session, workspace string) {
	if err := r.Log.WriteRun(state.record); err != nil {
		_ = r.Log.Write("run record update failed: " + err.Error())
	}
	if err := state.archive.WriteReport(state.report); err != nil {
		_ = r.Log.Write("report failed: " + err.Error())
	}
	cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	_ = r.Client.WorkspaceReportMetadata(cleanup, workspace, "", true)
	cancel()
	if state.notified == "" {
		return
	}
	title := "Review loop stopped"
	if state.report.ExitCode == ExitClean {
		title = "Review loop clean"
	}
	notify, cancelNotify := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancelNotify()
	_ = r.Client.NotificationShow(notify, title, state.notified)
}

// manifest records the resolved configuration with provenance, so a month-old run can be explained
// without the tree it ran against.
func (r Run) manifest(ctx context.Context, state *session, author, reviewer herdr.Agent, checkpoints Checkpoints) Manifest {
	record := state.record
	head := ""
	if checkpoints.Enabled {
		head, _ = checkpoints.git(ctx, nil, "rev-parse", "HEAD")
	}
	rounds := make([]ManifestRound, 0, r.Config.MaxIterations)
	for round := 1; round <= r.Config.MaxIterations; round++ {
		policy := Policy(r.Config.Rounds, round)
		rounds = append(rounds, ManifestRound{Round: round, Command: DescribeCommand(reviewer.Kind, r.Config.ReviewCommand, policy), Level: policy.Describe()})
	}
	return Manifest{
		Run:           record.ID,
		Plugin:        r.Version,
		Repository:    record.Repository,
		Started:       record.Started,
		Head:          head,
		Scope:         state.scope.Spec,
		ScopeCommand:  state.scope.Command(),
		Profile:       r.Profile.Name,
		ProfileLayer:  r.profileLayer(),
		Author:        describeForManifest(author),
		Reviewer:      describeForManifest(reviewer),
		MaxIterations: r.Config.MaxIterations,
		Retries:       r.Config.Retries,
		ReviewTimeout: r.Config.Timeouts.Review.String(),
		FixTimeout:    r.Config.Timeouts.Fix.String(),
		Rounds:        rounds,
	}
}

// profileLayer names where the round policy was read from. A profile no layer supplied leaves the
// built-in rounds in place, and saying so is more useful than naming a file that is not there.
func (r Run) profileLayer() string {
	if r.Profile.Layer == "" {
		return "not found"
	}
	return r.Profile.Layer
}

func describeForManifest(agent herdr.Agent) ManifestAgent {
	return ManifestAgent{Kind: agent.Kind, Name: agent.Name, PaneID: agent.PaneID}
}

// describe is the dry run: it resolves everything a real run would and prints it, taking no lock
// and writing nothing.
func (r Run) describe(author, reviewer herdr.Agent, runDir *RunDir, scope Scope) error {
	lines := []string{
		"author:   " + herdr.Describe(author),
		"reviewer: " + herdr.Describe(reviewer),
		"scope:    " + scope.Spec + "  (" + scope.Command() + ")",
		"profile:  " + r.Profile.Name + " (" + r.profileLayer() + ")",
		"run dir:  " + runDir.Absolute(RunSubdir),
	}
	// the round policy repeats its last entry, so listing past it would print the same line twice
	shown := min(r.Config.MaxIterations, max(len(r.Config.Rounds), 1))
	for round := 1; round <= shown; round++ {
		policy := Policy(r.Config.Rounds, round)
		lines = append(lines, fmt.Sprintf("round %d:  %s — %s", round, DescribeCommand(reviewer.Kind, r.Config.ReviewCommand, policy), policy.Describe()))
	}
	if shown < r.Config.MaxIterations {
		lines = append(lines, fmt.Sprintf("rounds %d-%d repeat round %d", shown+1, r.Config.MaxIterations, shown))
	}
	if _, err := fmt.Fprintln(os.Stdout, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("failed to write dry-run output: %w", err)
	}
	return nil
}

// pair resolves who writes and who reviews, and refuses upfront when either agent has no way to
// clear its session — a loop that cannot reset would review stale context from round two on.
func (r Run) pair(ctx context.Context, dryRun bool) (author, reviewer herdr.Agent, err error) {
	agents, err := r.Client.AgentList(ctx)
	if err != nil {
		return herdr.Agent{}, herdr.Agent{}, err
	}
	author, ok := herdr.Find(agents, r.Environment.Context.FocusedPaneID)
	if !ok {
		return herdr.Agent{}, herdr.Agent{}, errors.New("run this from your agent's pane")
	}
	var note func(string)
	if !dryRun {
		note = r.note
	}
	reviewer, err = herdr.PickReviewer(r.Config, agents, author, note)
	if err != nil {
		return herdr.Agent{}, herdr.Agent{}, err
	}
	if dryRun {
		return author, reviewer, nil
	}
	if err := ValidateResetCommand(author, r.Config.ResetCommand); err != nil {
		return herdr.Agent{}, herdr.Agent{}, err
	}
	if err := ValidateResetCommand(reviewer, r.Config.ResetCommand); err != nil {
		return herdr.Agent{}, herdr.Agent{}, err
	}
	return author, reviewer, nil
}

// ensurePanel makes the progress panel visible next to the author. Every failure here is logged and
// swallowed: the loop's work does not depend on anyone watching it.
func (r Run) ensurePanel(ctx context.Context, author herdr.Agent) {
	if _, live := LivePanel(r.Environment.StateDir, author.WorkspaceID); live {
		_ = r.Log.Write("using existing panel")
		return
	}
	pane, err := r.Client.PluginPaneOpen(ctx, author.PaneID, author.PaneID)
	if err != nil {
		_ = r.Log.Write("panel open failed: " + err.Error())
		return
	}
	layout, err := r.Client.PaneLayout(ctx, pane)
	if err != nil {
		_ = r.Log.Write("panel layout failed: " + err.Error())
		return
	}
	current, found := 0, false
	for _, candidate := range layout.Panes {
		if candidate.PaneID == pane {
			current, found = candidate.Rect.Width, true
			break
		}
	}
	if !found {
		_ = r.Log.Write("panel: " + pane + " is not in the layout yet")
		return
	}
	direction, amount, resize := ResizeDirection(current, PanelWidth(layout.Area.Width), layout.Area.Width)
	if !resize {
		return
	}
	if err := r.Client.PaneResize(ctx, pane, direction, amount); err != nil {
		_ = r.Log.Write("panel resize failed: " + err.Error())
	}
}

// checkpoints prepares worktree snapshots for the run and takes the baseline one. A repository that
// is not a git work tree simply runs without them.
func (r Run) checkpoints(ctx context.Context, repository, runID string) Checkpoints {
	points := Checkpoints{Repository: repository, RunID: runID, Note: func(message string) { _ = r.Log.Write(message) }}
	points.Enabled = points.Available(ctx)
	if !points.Enabled {
		_ = r.Log.Write("no git work tree: running without checkpoints")
		return points
	}
	// pruning happens before this run's baseline exists, so it has to reserve a
	// slot for it; keeping the full retention here would leave one run more than
	// CheckpointRetention behind for every run that completes.
	if err := points.Prune(ctx, CheckpointRetention-1); err != nil {
		_ = r.Log.Write("checkpoint prune failed: " + err.Error())
	}
	points.SaveQuietly(ctx, 0)
	return points
}

func (r Run) reportProgress(ctx context.Context, workspace, phase string, round int) {
	token := fmt.Sprintf("%s %d/%d", phase, round, r.Config.MaxIterations)
	if err := r.Client.WorkspaceReportMetadata(ctx, workspace, token, false); err != nil {
		_ = r.Log.Write("progress update failed: " + err.Error())
	}
}

func (r Run) note(message string) { _ = r.Log.Write(message) }

func questionSummary(questions []OpenQuestion) string {
	texts := make([]string, 0, len(questions))
	for _, question := range questions {
		texts = append(texts, question.Question)
	}
	return strings.Join(texts, " · ")
}

func decisionSummary(decisions Decisions) string {
	counts := decisions.Counts()
	return fmt.Sprintf("%d applied · %d rejected · %d deferred · %d missing",
		counts[ActionApplied], counts[ActionRejected], counts[ActionDeferred], counts[ActionMissing])
}
