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

// Run is one invocation of the review loop, wired with everything it needs to execute.
type Run struct {
	Client      Client
	Config      config.Values
	Environment herdr.Environment
	Log         Log
}

const cleanupTimeout = 2 * time.Second

// verdictWait is how long a phase waits for the file after the agent's turn ends. The agent has
// already stopped by then, so this only covers the write landing on disk.
const verdictWait = 15 * time.Second

// Execute runs review and apply rounds until the reviewer reports clean or the iteration budget is
// spent. It holds the run lock for its whole duration, so only one loop runs per state directory.
func (r Run) Execute(ctx context.Context, dryRun bool) error {
	author, reviewer, err := r.pair(ctx, dryRun)
	if err != nil {
		return err
	}
	repository, err := r.Environment.Repository()
	if err != nil {
		return err
	}
	review, summary, err := r.files(repository)
	if err != nil {
		return err
	}
	defer func() { _ = review.Close() }()
	defer func() { _ = summary.Close() }()
	if dryRun {
		if _, err := fmt.Fprintf(os.Stdout, "author: %s\nreviewer: %s\n", herdr.Describe(author), herdr.Describe(reviewer)); err != nil {
			return fmt.Errorf("failed to write dry-run output: %w", err)
		}
		return nil
	}
	if err := r.Log.Write(fmt.Sprintf("author %s; review by %s", herdr.Describe(author), herdr.Describe(reviewer))); err != nil {
		return err
	}
	lock, lockErr := AcquireLock(r.Environment.StateDir, author.WorkspaceID)
	if lockErr != nil {
		return lockErr
	}
	if err := summary.Remove(); err != nil {
		_ = lock.Release()
		return err
	}
	record := RunRecord{ID: NewRunID(time.Now()), Repository: repository, Author: herdr.Describe(author), Reviewer: herdr.Describe(reviewer), Started: time.Now().UTC(), Outcome: "running"}
	if err := r.Log.WriteRun(record); err != nil {
		_ = lock.Release()
		return err
	}
	var notificationTitle, notificationMessage string
	defer func() {
		if err := r.Log.WriteRun(record); err != nil {
			_ = r.Log.Write("run record update failed: " + err.Error())
		}
		cleanup, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		_ = r.Client.WorkspaceReportMetadata(cleanup, author.WorkspaceID, "", true)
		cancel()
		_ = lock.Release()
		if notificationTitle != "" {
			notify, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			_ = r.Client.NotificationShow(notify, notificationTitle, notificationMessage)
		}
	}()
	abort := func(err error) error {
		notificationTitle = "Review loop stopped"
		notificationMessage = r.abortMessage(err)
		record.Outcome = notificationMessage
		return err
	}
	r.ensurePanel(ctx, author)
	checkpoints := r.checkpoints(ctx, repository, record.ID)
	for iteration := 1; iteration <= r.Config.MaxIterations; iteration++ {
		record.Rounds = iteration
		r.reportProgress(ctx, author.WorkspaceID, "review", iteration)
		_ = r.Log.Write(fmt.Sprintf("--- iteration %d/%d: review", iteration, r.Config.MaxIterations))
		contents, verdict, err := r.reviewRound(ctx, reviewer, review, summary, iteration)
		if err != nil {
			return abort(err)
		}
		if err := r.Log.Archive(record.ID, iteration, contents); err != nil {
			return abort(err)
		}
		if verdict == Clean {
			_ = r.Client.AgentFocus(ctx, herdr.Target(author))
			outcome := fmt.Sprintf("clean after %d iteration(s)", iteration)
			_ = r.Log.Write(outcome)
			notificationTitle, notificationMessage = "Review loop clean", outcome
			record.Outcome = "clean"
			return nil
		}
		_ = r.Log.Write(fmt.Sprintf("findings reported (%d lines)", len(splitLines(contents))))
		if iteration == r.Config.MaxIterations {
			break
		}
		r.reportProgress(ctx, author.WorkspaceID, "fix", iteration)
		_ = r.Log.Write(fmt.Sprintf("--- iteration %d/%d: apply", iteration, r.Config.MaxIterations))
		if err := r.applyRound(ctx, author, review, summary, iteration); err != nil {
			return abort(err)
		}
		checkpoints.SaveQuietly(ctx, iteration)
	}
	exhausted := errors.New("stopped after max iterations with findings still open")
	_ = r.Log.Write(exhausted.Error())
	notificationTitle, notificationMessage = "Review loop stopped", exhausted.Error()
	record.Outcome = "findings still open"
	return exhausted
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

// files opens the two files the round exchanges through. Both are returned open; the caller closes
// them.
func (r Run) files(repository string) (review, summary *ReviewFile, err error) {
	if r.Config.ReviewFile == SummaryFile {
		return nil, nil, fmt.Errorf("review_file must not be %s; it is reserved for review decisions", SummaryFile)
	}
	review, err = OpenReviewFile(repository, r.Config.ReviewFile)
	if err != nil {
		return nil, nil, err
	}
	summary, err = OpenSummaryFile(repository)
	if err != nil {
		_ = review.Close()
		return nil, nil, err
	}
	return review, summary, nil
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

func (r Run) reportProgress(ctx context.Context, workspace, phase string, iteration int) {
	token := fmt.Sprintf("%s %d/%d", phase, iteration, r.Config.MaxIterations)
	if err := r.Client.WorkspaceReportMetadata(ctx, workspace, token, false); err != nil {
		_ = r.Log.Write("progress update failed: " + err.Error())
	}
}

// reviewRound has the reviewer read the working tree and write its verdict. The old review file is
// removed first and freshness is judged against askedAt, so the previous round's findings can never
// be mistaken for this round's.
func (r Run) reviewRound(ctx context.Context, reviewer herdr.Agent, review, summary *ReviewFile, iteration int) (string, Verdict, error) {
	if err := r.Client.AgentFocus(ctx, herdr.Target(reviewer)); err != nil {
		return "", Findings, err
	}
	phase, cancel := context.WithTimeout(ctx, r.Config.ReviewTimeout)
	defer cancel()
	_, err := Settle(phase, r.Client, reviewer)
	if err == nil {
		err = ResetSession(phase, r.Client, reviewer, r.Config.ResetCommand, r.note)
	}
	if err == nil {
		_, err = Settle(phase, r.Client, reviewer)
	}
	if err == nil {
		err = review.EnsureParent()
	}
	if err == nil {
		err = review.Remove()
	}
	askedAt := time.Now()
	if err == nil {
		_, err = SubmitAndWait(phase, r.Client, reviewer, ReviewPrompt(review.Absolute, summary.Absolute, iteration, r.Config.MaxIterations))
	}
	if err != nil {
		return "", Findings, err
	}
	return review.WaitForVerdict(phase, askedAt, verdictWait)
}

// applyRound has the author act on the findings and record what it decided. The summary is the
// proof of work: an author that changes nothing and writes nothing fails the round.
func (r Run) applyRound(ctx context.Context, author herdr.Agent, review, summary *ReviewFile, iteration int) error {
	if err := r.Client.AgentFocus(ctx, herdr.Target(author)); err != nil {
		return err
	}
	phase, cancel := context.WithTimeout(ctx, r.Config.FixTimeout)
	defer cancel()
	err := summary.EnsureRegular()
	if err == nil {
		_, err = Settle(phase, r.Client, author)
	}
	if err == nil {
		err = ResetSession(phase, r.Client, author, r.Config.ResetCommand, r.note)
	}
	if err == nil {
		_, err = Settle(phase, r.Client, author)
	}
	askedAt := time.Now()
	if err == nil {
		_, err = SubmitAndWait(phase, r.Client, author, FixPrompt(review.Absolute, summary.Absolute, iteration))
	}
	if err == nil {
		if _, waitErr := summary.WaitForChange(phase, askedAt, verdictWait); waitErr != nil {
			err = fmt.Errorf("author settled without updating %s: %w", summary.Relative, waitErr)
		}
	}
	return err
}

func (r Run) note(message string) { _ = r.Log.Write(message) }

func (r Run) abortMessage(err error) string {
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "canceled"
	}
	_ = r.Log.Write(message)
	return message
}

func splitLines(contents string) []string {
	if contents == "" {
		return nil
	}
	return strings.Split(contents, "\n")
}
