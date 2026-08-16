package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RunRecord describes one review run. It lives beside that run's archive so history survives the
// working tree it was produced from: the repository path is recorded because the state directory is
// shared by every repository the plugin has ever run in.
type RunRecord struct {
	ID         string    `json:"id"`
	Repository string    `json:"repository"`
	Author     string    `json:"author"`
	Reviewer   string    `json:"reviewer"`
	Started    time.Time `json:"started"`
	Rounds     int       `json:"rounds"`
	Outcome    string    `json:"outcome"`
}

// runIDLayout is RFC 3339 with a fixed-width fraction. The width is what makes
// the id sortable: RFC3339Nano trims trailing zeros, so ".1Z" would sort after
// the later ".12Z" and both after an exact second. Everything downstream —
// ListRuns, LatestRun, rotation, checkpoint pruning — orders runs lexicographically.
const runIDLayout = "2006-01-02T15:04:05.000000000Z07:00"

// NewRunID names a run after the instant it started, so run ids sort chronologically and history
// can be ordered without reading a single record.
func NewRunID(at time.Time) string {
	return strings.NewReplacer(":", "-", ".", "-").Replace(at.UTC().Format(runIDLayout))
}

// WriteRun publishes a run's record, replacing any earlier version of it.
func (l Log) WriteRun(record RunRecord) error {
	dir := ArchiveDir(l.StateDir, record.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}
	return writeJSON(filepath.Join(dir, runRecordFile), record)
}

// ReadRun loads one run's record.
func ReadRun(stateDir, runID string) (RunRecord, error) {
	path := filepath.Join(ArchiveDir(stateDir, runID), runRecordFile)
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

// RoundView is one review round as history can still see it: the archived
// review always survives in the state directory, while the diff depends on the
// repository and on checkpoints that retention may have already dropped.
type RoundView struct {
	Number   int
	Clean    bool
	Blocked  bool
	Findings int
	Applied  int
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

// Browse assembles every recorded run for display. Reading a round's parsed review is cheap and
// bounded, and doing it here keeps the pane free of file layout knowledge.
func Browse(ctx context.Context, stateDir string) []RunView {
	records := ListRuns(stateDir)
	checkpoints := checkpointRounds(ctx, records)
	views := make([]RunView, 0, len(records))
	for _, record := range records {
		view := RunView{Record: record}
		commits := checkpoints[record.Repository][record.ID]
		view.baseline = commits[0]
		for _, number := range ArchivedRounds(stateDir, record.ID) {
			round := RoundView{Number: number, Commit: commits[number], Base: commits[number-1]}
			if document, found := LoadRound(stateDir, record.ID, number); found {
				round.Clean = document.Review.Resolve() == Clean
				round.Blocked = document.Review.Resolve() == Blocked
				round.Findings = len(document.Review.Findings)
				round.Applied = document.Decisions.Counts()[ActionApplied]
			}
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

// marshalIndent encodes a value the way every file the loop writes is encoded: indented, so a
// human opening one in an editor can read it.
func marshalIndent(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to encode: %w", err)
	}
	return append(data, '\n'), nil
}

func writeJSON(path string, value any) error {
	data, err := marshalIndent(value)
	if err != nil {
		return fmt.Errorf("failed to encode %s: %w", path, err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".record-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file next to %s: %w", path, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = temp.Write(data); err == nil {
		err = temp.Chmod(0o600)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("failed to write %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("failed to install %s: %w", path, err)
	}
	return nil
}
