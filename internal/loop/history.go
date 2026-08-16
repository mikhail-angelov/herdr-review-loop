package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RunRecord describes one review run. It lives beside that run's archived
// verdicts so history survives the working tree it was produced from: the
// repository path is recorded because the state directory is shared by every
// repository the plugin has ever run in.
type RunRecord struct {
	ID         string    `json:"id"`
	Repository string    `json:"repository"`
	Author     string    `json:"author"`
	Reviewer   string    `json:"reviewer"`
	Started    time.Time `json:"started"`
	Rounds     int       `json:"rounds"`
	Outcome    string    `json:"outcome"`
}

const runRecordFile = "run.json"
const summaryArchive = "summary.md"

// runIDLayout is RFC 3339 with a fixed-width fraction. The width is what makes
// the id sortable: RFC3339Nano trims trailing zeros, so ".1Z" would sort after
// the later ".12Z" and both after an exact second. Everything downstream —
// ListRuns, LatestRun, checkpoint pruning — orders runs lexicographically.
const runIDLayout = "2006-01-02T15:04:05.000000000Z07:00"

// NewRunID names a run after the instant it started, so run ids sort chronologically and history
// can be ordered without reading a single record.
func NewRunID(at time.Time) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(at.UTC().Format(runIDLayout))
}

func historyDir(stateDir string) string { return filepath.Join(stateDir, "history") }

// RunDir is where a run's record, verdicts and summary are archived.
func RunDir(stateDir, runID string) string { return filepath.Join(historyDir(stateDir), runID) }

// WriteRun publishes a run's record, replacing any earlier version of it.
func (l Log) WriteRun(record RunRecord) error {
	dir := RunDir(l.StateDir, record.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	return writeJSON(filepath.Join(dir, runRecordFile), record)
}

// ArchiveSummary keeps the author's decision record with the rest of the run.
func (l Log) ArchiveSummary(runID, contents string) error {
	dir := RunDir(l.StateDir, runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	path := filepath.Join(dir, summaryArchive)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// ReadRun loads one run's record.
func ReadRun(stateDir, runID string) (RunRecord, error) {
	path := filepath.Join(RunDir(stateDir, runID), runRecordFile)
	data, err := os.ReadFile(path) //nolint:gosec // path is built from the plugin's own state directory
	if err != nil {
		return RunRecord{}, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var record RunRecord
	if err := json.Unmarshal(data, &record); err != nil || record.ID == "" {
		return RunRecord{}, fmt.Errorf("invalid run record in %s", path)
	}
	return record, nil
}

// ListRuns returns every run that has a record, newest first. Run ids are
// timestamps, so ordering by id is chronological without reading any file.
func ListRuns(stateDir string) []RunRecord {
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
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	runs := make([]RunRecord, 0, len(names))
	for _, name := range names {
		record, err := ReadRun(stateDir, name)
		if err != nil {
			continue
		}
		runs = append(runs, record)
	}
	return runs
}

// LatestRun is the most recent run recorded for a repository, which is the one finish reports on.
func LatestRun(stateDir, repository string) (RunRecord, bool) {
	for _, record := range ListRuns(stateDir) {
		if record.Repository == repository {
			return record, true
		}
	}
	return RunRecord{}, false
}

// Verdicts lists the archived review files of a run, in round order.
func Verdicts(stateDir, runID string) []string {
	entries, err := os.ReadDir(RunDir(stateDir, runID))
	if err != nil {
		return nil
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "iteration-") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files
}

// RoundView is one review round as history can still see it: the archived
// verdict always survives in the state directory, while the diff depends on the
// repository and on checkpoints that retention may have already dropped.
type RoundView struct {
	Number   int
	Verdict  string
	Clean    bool
	Findings int
	Commit   string
	Base     string
}

// HasDiff reports whether both ends of this round's diff still exist.
func (r RoundView) HasDiff() bool { return r.Commit != "" && r.Base != "" }

// RunView is one run as the history pane shows it: its record plus a view of every round.
type RunView struct {
	Record   RunRecord
	Rounds   []RoundView
	baseline string
}

// Browse assembles every recorded run for display. Reading a verdict is cheap
// and bounded, and doing it here keeps the pane free of file layout knowledge.
func Browse(ctx context.Context, stateDir string) []RunView {
	records := ListRuns(stateDir)
	checkpoints := checkpointRounds(ctx, records)
	views := make([]RunView, 0, len(records))
	for _, record := range records {
		view := RunView{Record: record}
		commits := checkpoints[record.Repository][record.ID]
		view.baseline = commits[0]
		for _, name := range Verdicts(stateDir, record.ID) {
			round := RoundView{Number: roundOfVerdict(name), Verdict: filepath.Join(RunDir(stateDir, record.ID), name)}
			contents, err := os.ReadFile(round.Verdict)
			if err == nil {
				round.Clean = ParseVerdict(string(contents)) == Clean
				round.Findings = countFindings(string(contents))
			}
			round.Commit = commits[round.Number]
			round.Base = commits[round.Number-1]
			view.Rounds = append(view.Rounds, round)
		}
		views = append(views, view)
	}
	return views
}

// checkpointRounds reads refs once per repository instead of once per recorded
// run. A shared state directory can contain many runs from the same repository,
// so this keeps opening history from spawning a git process for every row.
func checkpointRounds(ctx context.Context, records []RunRecord) map[string]map[string]map[int]string {
	byRepository := make(map[string]map[string]map[int]string)
	for _, record := range records {
		if _, seen := byRepository[record.Repository]; seen {
			continue
		}
		byRun := make(map[string]map[int]string)
		refs, err := (Checkpoints{Repository: record.Repository}).refs(ctx, RefPrefix)
		if err == nil {
			for ref, commit := range refs {
				runID, isRunRef := runOfRef(ref)
				round, isRoundRef := roundOfRef(ref)
				if !isRunRef || !isRoundRef {
					continue
				}
				if byRun[runID] == nil {
					byRun[runID] = make(map[int]string)
				}
				byRun[runID][round] = commit
			}
		}
		byRepository[record.Repository] = byRun
	}
	return byRepository
}

// Baseline is the snapshot taken before the first fix, and the only thing a
// whole-run diff can be measured against.
func (v RunView) Baseline() string { return v.baseline }

// Last is the newest round of this run that still has a checkpoint.
func (v RunView) Last() string {
	for index := len(v.Rounds) - 1; index >= 0; index-- {
		if v.Rounds[index].Commit != "" {
			return v.Rounds[index].Commit
		}
	}
	return ""
}

func roundOfVerdict(name string) int {
	value := strings.TrimSuffix(strings.TrimPrefix(name, "iteration-"), ".md")
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}

func countFindings(contents string) int {
	count := 0
	for _, line := range strings.Split(contents, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- [") {
			count++
		}
	}
	return count
}

// SummaryStats counts the decisions the author recorded for a run.
type SummaryStats struct {
	Applied, Rejected, Deferred int
	Tests                       string
}

// Decisions is how many findings the author accounted for, one way or another.
func (s SummaryStats) Decisions() int { return s.Applied + s.Rejected + s.Deferred }

// ParseSummary reads the decision record the author is asked to write. The
// markers are a prompt contract, not a guarantee: an unmarked record yields
// zero counts rather than an error, because a digest is not worth failing over.
func ParseSummary(contents string) SummaryStats {
	var stats SummaryStats
	for _, line := range strings.Split(contents, "\n") {
		value := strings.ToLower(strings.TrimLeft(strings.TrimSpace(line), "-*• \t"))
		switch {
		case strings.HasPrefix(value, "applied:"):
			stats.Applied++
		case strings.HasPrefix(value, "rejected:"):
			stats.Rejected++
		case strings.HasPrefix(value, "deferred:"):
			stats.Deferred++
		case strings.HasPrefix(value, "tests:"):
			stats.Tests = strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(strings.TrimSpace(line), "-*• \t"), "tests:"))
		}
	}
	return stats
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", path, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file next to %s: %w", path, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = temp.Write(append(data, '\n')); err == nil {
		err = temp.Close()
	} else {
		_ = temp.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("failed to install %s: %w", path, err)
	}
	return nil
}
