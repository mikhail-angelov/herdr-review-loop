package loop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Archive is one run's durable record, under the plugin state directory. The repository is not the
// place for tens of megabytes of raw model output, and nothing here is ever in the reviewed diff.
type Archive struct {
	Dir string
	// RawOutput off drops every *.raw.txt. It wins everywhere, including over the degradation
	// ladder: a round whose output never parsed still records the failure as an event, but the raw
	// text is not retained, because a privacy setting that has exceptions is not one.
	RawOutput bool
}

// The archive's file names. They are constants because the renderer and the history pane read
// what the run wrote, and one misspelling between them would be a silently empty round.
const (
	manifestFile   = "manifest.json"
	eventsFile     = "events.jsonl"
	reportFile     = "report.json"
	promptReview   = "prompt-review.md"
	promptFix      = "prompt-fix.md"
	rawReview      = "review.raw.txt"
	rawFix         = "fix.raw.txt"
	changesPatch   = "changes.patch"
	runRecordFile  = "run.json"
	roundDirPrefix = "round-"
)

// ArchiveDir is where a run's record, rounds and report are kept.
func ArchiveDir(stateDir, runID string) string {
	return filepath.Join(stateDir, "history", runID)
}

func historyDir(stateDir string) string { return filepath.Join(stateDir, "history") }

// OpenArchive creates a run's archive directory.
func OpenArchive(stateDir, runID string, rawOutput bool) (*Archive, error) {
	dir := ArchiveDir(stateDir, runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create archive %s: %w", dir, err)
	}
	return &Archive{Dir: dir, RawOutput: rawOutput}, nil
}

// ManifestAgent identifies one side of the pair well enough to explain a month-old run.
type ManifestAgent struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	PaneID  string `json:"pane_id"`
	Version string `json:"version,omitempty"`
}

// ManifestRound records what the round policy asked of the reviewer, so a run can be explained
// without guessing which prompt it was driven by.
type ManifestRound struct {
	Round   int    `json:"round"`
	Command string `json:"command"`
	Level   string `json:"level"`
}

// Manifest is the resolved configuration with provenance.
type Manifest struct {
	Run           string          `json:"run"`
	Plugin        string          `json:"plugin_version"`
	Repository    string          `json:"repository"`
	Started       time.Time       `json:"started"`
	Head          string          `json:"head"`
	Scope         string          `json:"scope"`
	ScopeCommand  string          `json:"scope_command"`
	Profile       string          `json:"profile"`
	ProfileLayer  string          `json:"profile_layer"`
	Author        ManifestAgent   `json:"author"`
	Reviewer      ManifestAgent   `json:"reviewer"`
	MaxIterations int             `json:"max_iterations"`
	Retries       int             `json:"retries"`
	ReviewTimeout string          `json:"review_timeout"`
	FixTimeout    string          `json:"fix_timeout"`
	Rounds        []ManifestRound `json:"rounds"`
}

// WriteManifest publishes the manifest, replacing any earlier version. It is rewritten as rounds
// are added, so a run interrupted halfway still explains the rounds it managed.
func (a *Archive) WriteManifest(manifest Manifest) error {
	return writeJSON(filepath.Join(a.Dir, manifestFile), manifest)
}

// Event is one line of the run's event stream. The panel consumes this stream instead of parsing
// the log, which is also how it gets stall and retry lines for free.
type Event struct {
	TS     time.Time `json:"ts"`
	Round  int       `json:"round"`
	Phase  string    `json:"phase"`
	Event  string    `json:"event"`
	Detail string    `json:"detail,omitempty"`
}

// The events a run emits. Anything the panel or a later diagnosis needs to distinguish is one of
// these; everything else is a detail string on one of them.
const (
	EventPhaseStart    = "phase_start"
	EventPhaseDone     = "phase_done"
	EventStall         = "stall"
	EventRetry         = "retry"
	EventTimeout       = "timeout"
	EventBlocked       = "blocked"
	EventParseFallback = "parse_fallback"
	EventDegraded      = "degraded"
	EventCanceled      = "canceled"
	// EventFiltered names the findings min_verdict withheld from the author, so a round that came
	// back clean because nothing cleared the bar is never confused with one nothing was found in.
	EventFiltered = "filtered"
)

// Event appends one line to the run's event stream.
func (a *Archive) Event(round int, phase, event, detail string) error {
	line, err := json.Marshal(Event{TS: time.Now().UTC(), Round: round, Phase: phase, Event: event, Detail: detail})
	if err != nil {
		return fmt.Errorf("failed to encode event: %w", err)
	}
	path := filepath.Join(a.Dir, eventsFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // the path is built from the plugin's own state directory
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	_, writeErr := file.Write(append(line, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("failed to write %s: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close %s: %w", path, closeErr)
	}
	return nil
}

// ReadEvents returns a run's events in the order they happened, or nothing when there are none.
func ReadEvents(dir string) []Event {
	data, err := os.ReadFile(filepath.Join(dir, eventsFile)) //nolint:gosec // the path is built from the plugin's own state directory
	if err != nil {
		return nil
	}
	var events []Event
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var event Event
		if err := decoder.Decode(&event); err != nil {
			return events
		}
		events = append(events, event)
	}
}

// roundDir creates and returns one round's directory inside the archive.
// roundName is the archive's directory for one round, named in one place so a reader and a writer
// cannot disagree about it.
func roundName(round int) string { return fmt.Sprintf("%s%02d", roundDirPrefix, round) }

func (a *Archive) roundDir(round int) (string, error) {
	dir := filepath.Join(a.Dir, roundName(round))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create %s: %w", dir, err)
	}
	return dir, nil
}

// Prompt keeps what an agent was sent, verbatim.
func (a *Archive) Prompt(round int, phase, text string) error {
	return a.PromptAttempt(round, phase, 0, false, text)
}

// Raw keeps what an agent answered, verbatim, unless raw output is turned off.
func (a *Archive) Raw(round int, phase, text string) error {
	return a.RawAttempt(round, phase, 0, false, text)
}

// PromptAttempt keeps every command sent in a phase. Retries and reformat turns get their own
// file rather than overwriting the first prompt, which is needed to explain a degraded round.
func (a *Archive) PromptAttempt(round int, phase string, attempt int, reformat bool, text string) error {
	return a.writeRound(round, turnFile(promptFile(phase), attempt, reformat), text)
}

// RawAttempt keeps every answer in a phase, unless raw output is turned off. Its file name stays
// paired with PromptAttempt's, so a failed attempt remains diagnosable after a later retry works.
func (a *Archive) RawAttempt(round int, phase string, attempt int, reformat bool, text string) error {
	if !a.RawOutput {
		return nil
	}
	return a.writeRound(round, turnFile(rawFile(phase), attempt, reformat), text)
}

func promptFile(phase string) string {
	switch phase {
	case PhaseFix:
		return promptFix
	case PhaseBrief:
		return "prompt-brief.md"
	case PhaseOneShotAction:
		return "prompt-one-shot-action.md"
	}
	return promptReview

}

func rawFile(phase string) string {
	switch phase {
	case PhaseFix:
		return rawFix
	case PhaseBrief:
		return "brief.raw.txt"
	case PhaseOneShotAction:
		return "one-shot-action.raw.txt"
	}
	return rawReview
}

func turnFile(name string, attempt int, reformat bool) string {
	if attempt == 0 && !reformat {
		return name
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	if strings.HasSuffix(name, ".raw.txt") {
		extension = ".raw.txt"
		stem = strings.TrimSuffix(name, extension)
	}
	if reformat {
		return fmt.Sprintf("%s-reformat-%02d%s", stem, attempt+1, extension)
	}
	return fmt.Sprintf("%s-retry-%02d%s", stem, attempt, extension)
}

// Parsed keeps the loop's own reading of an agent's output: the review with its ids and
// fingerprints, and the decisions completed with the ids the author left out.
func (a *Archive) Parsed(round int, name string, value any) error {
	dir, err := a.roundDir(round)
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, name), value)
}

// Patch keeps everything the author phase changed, new files included.
func (a *Archive) Patch(round int, patch string) error {
	return a.writeRound(round, changesPatch, patch)
}

// Patches concatenates every round's changes.patch up to but not including the given round. It is
// the baseline a regressions-only pass is judged against: what the fixes so far actually changed.
// A round with no patch contributes nothing, so an interrupted round does not break the baseline.
func (a *Archive) Patches(before int) string {
	var baseline strings.Builder
	for round := 1; round < before; round++ {
		data, err := os.ReadFile(filepath.Join(a.Dir, roundName(round), changesPatch))
		if err != nil || len(data) == 0 {
			continue
		}
		fmt.Fprintf(&baseline, "### round %d\n\n", round)
		baseline.Write(data)
		if !strings.HasSuffix(string(data), "\n") {
			baseline.WriteString("\n")
		}
		baseline.WriteString("\n")
	}
	return baseline.String()
}

func (a *Archive) writeRound(round int, name, text string) error {
	dir, err := a.roundDir(round)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// ReportFinding is one finding and what became of it. Every finding the reviewer ever raised gets
// exactly one entry, whatever became of it: a report that listed only the findings someone
// answered would be silent about exactly the rounds that went wrong.
type ReportFinding struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Round       int    `json:"round"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Verdict     string `json:"verdict"`
	Title       string `json:"title"`
	Action      string `json:"action"`
	Note        string `json:"note"`
}

// Report is the run's outcome in one file, and the file to read when asking what a run did.
type Report struct {
	Run           string          `json:"run"`
	ExitCode      int             `json:"exit_code"`
	Outcome       string          `json:"outcome"`
	Rounds        int             `json:"rounds"`
	Findings      []ReportFinding `json:"findings"`
	OpenQuestions []OpenQuestion  `json:"open_questions,omitempty"`
	Degraded      []string        `json:"degraded,omitempty"`
}

// WriteReport publishes the run's outcome.
func (a *Archive) WriteReport(report Report) error {
	return writeJSON(filepath.Join(a.Dir, reportFile), report)
}

// ReadReport loads a run's outcome, which is what the renderer and the closing digest report on.
func ReadReport(stateDir, runID string) (Report, bool) {
	data, err := os.ReadFile(filepath.Join(ArchiveDir(stateDir, runID), reportFile)) //nolint:gosec // the path is built from the plugin's own state directory
	if err != nil {
		return Report{}, false
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		return Report{}, false
	}
	return report, true
}

// ReadManifest loads a run's resolved configuration, which is what explains a month-old run
// without the tree it ran against.
func ReadManifest(stateDir, runID string) (Manifest, bool) {
	data, err := os.ReadFile(filepath.Join(ArchiveDir(stateDir, runID), manifestFile)) //nolint:gosec // the path is built from the plugin's own state directory
	if err != nil {
		return Manifest{}, false
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, false
	}
	return manifest, true
}

// ReadRoundReview loads a round's parsed review out of the archive.
func ReadRoundReview(stateDir, runID string, round int) (Review, bool) {
	var review Review
	found := readRoundJSON(stateDir, runID, round, reviewFile, &review)
	return review, found
}

// ReadRoundDecisions loads a round's completed decisions out of the archive.
func ReadRoundDecisions(stateDir, runID string, round int) (Decisions, bool) {
	var decisions Decisions
	found := readRoundJSON(stateDir, runID, round, decisionsFile, &decisions)
	return decisions, found
}

func readRoundJSON(stateDir, runID string, round int, name string, into any) bool {
	path := filepath.Join(ArchiveDir(stateDir, runID), fmt.Sprintf("%s%02d", roundDirPrefix, round), name)
	data, err := os.ReadFile(path) //nolint:gosec // the path is built from the plugin's own state directory
	if err != nil {
		return false
	}
	return json.Unmarshal(data, into) == nil
}

// ArchivedRounds lists the rounds a run archived, in order.
func ArchivedRounds(stateDir, runID string) []int {
	entries, err := os.ReadDir(ArchiveDir(stateDir, runID))
	if err != nil {
		return nil
	}
	var rounds []int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var round int
		if _, err := fmt.Sscanf(entry.Name(), roundDirPrefix+"%d", &round); err == nil {
			rounds = append(rounds, round)
		}
	}
	sort.Ints(rounds)
	return rounds
}

// Rotate drops the oldest run archives beyond keep. Nothing outlives rotation: an archive is for
// explaining a run that just happened or one from last week, not a dataset. Raising archive.keep
// is how someone who wants more history gets it.
func Rotate(stateDir string, keep int) error {
	if keep < 0 {
		keep = 0
	}
	entries, err := os.ReadDir(historyDir(stateDir))
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	// run ids are timestamps, so the newest sort last
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for index, name := range names {
		if index < keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(historyDir(stateDir), name)); err != nil {
			return fmt.Errorf("failed to rotate archive %s: %w", name, err)
		}
	}
	return nil
}
