package loop

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeRepoFile(t *testing.T, repository, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openArchive opens a run's archive or fails the test.
func openArchive(t *testing.T, state, runID string) *Archive {
	t.Helper()
	archive, err := OpenArchive(state, runID, true)
	if err != nil {
		t.Fatal(err)
	}
	return archive
}

// archiveRound writes one round into a run's archive the way a run would, so tests that read the
// archive back exercise the same layout the loop writes.
func archiveRound(t *testing.T, state, runID string, round int, review Review, decisions Decisions) {
	t.Helper()
	archive := openArchive(t, state, runID)
	if err := archive.Parsed(round, reviewFile, review); err != nil {
		t.Fatal(err)
	}
	if len(decisions.Decisions) > 0 || decisions.Tests.Outcome != "" {
		if err := archive.Parsed(round, decisionsFile, decisions); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFinishRemovesTheRunDirectoryAndReportsTheArchive(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	record := RunRecord{ID: "2026-08-09T10-00-00Z", Repository: repository, Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Rounds: 2, Outcome: "clean", Started: time.Now().UTC()}
	if err := (Log{StateDir: state}).WriteRun(record); err != nil {
		t.Fatal(err)
	}
	archive := openArchive(t, state, record.ID)
	report := Report{Run: record.ID, Rounds: 2, Outcome: "clean after 2 round(s)", Findings: []ReportFinding{
		{ID: "r01-1", Action: ActionApplied},
		{ID: "r01-2", Action: ActionRejected},
		{ID: "r01-3", Action: ActionMissing},
	}}
	if err := archive.WriteReport(report); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, RunSubdir, "round-01"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repository, "code.go", "package main\n")

	result, err := Finish(state, "workspace", repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repository, RunSubdir)); !os.IsNotExist(err) {
		t.Fatalf("the run directory survived finish: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repository, "code.go")); err != nil {
		t.Fatalf("finish deleted the author's work: %v", err)
	}
	digest := strings.Join(result.Digest(), "\n")
	for _, want := range []string{"2 round(s)", "clean", "1 applied · 1 rejected · 0 deferred · 1 missing", "archived to history/" + record.ID, "removed " + RunSubdir} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest missing %q: %s", want, digest)
		}
	}
}

func TestFinishRefusesWhileALoopHoldsTheLock(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	lock, err := AcquireLock(state, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if err := os.MkdirAll(filepath.Join(repository, RunSubdir), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := Finish(state, "workspace", repository); err == nil {
		t.Fatal("finish deleted files underneath a running loop")
	}
	if _, err := os.Lstat(filepath.Join(repository, RunSubdir)); err != nil {
		t.Fatalf("the run directory was removed despite the refusal: %v", err)
	}
}

func TestFinishReportsNothingToDoOnACleanTree(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	result, err := Finish(state, "workspace", repository)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Empty() {
		t.Fatalf("expected an empty result, got %+v", result)
	}
	if got := result.Digest(); len(got) != 1 || got[0] != "nothing to finish" {
		t.Fatalf("unexpected digest: %v", got)
	}
}

func TestBrowsePairsRoundsWithTheCheckpointsThatFollowThem(t *testing.T) {
	state := t.TempDir()
	repository := newRepository(t)
	log := Log{StateDir: state}
	record := RunRecord{ID: "2026-08-09T10-00-00Z", Repository: repository, Author: "claude", Reviewer: "codex", Rounds: 2, Outcome: "clean"}
	if err := log.WriteRun(record); err != nil {
		t.Fatal(err)
	}
	first := Review{Status: StatusFindings, Findings: []Finding{{File: "a.go", Line: 1, Title: "wrong"}, {File: "b.go", Line: 2, Title: "nit"}}}
	first.Identify(1)
	archiveRound(t, state, record.ID, 1, first, Decisions{Decisions: []Decision{{ID: "r01-1", Action: ActionApplied}, {ID: "r01-2", Action: ActionRejected}}})
	archiveRound(t, state, record.ID, 2, Review{Status: StatusClean}, Decisions{})
	// A baseline and one applied round: round 1 is diffable, round 2 is not.
	checkpoints := Checkpoints{Repository: repository, RunID: record.ID, Enabled: true}
	for _, round := range []int{0, 1} {
		writeRepoFile(t, repository, "tracked.txt", strconv.Itoa(round))
		if _, err := checkpoints.Save(t.Context(), round); err != nil {
			t.Fatal(err)
		}
	}

	views := Browse(t.Context(), state)
	if len(views) != 1 || len(views[0].Rounds) != 2 {
		t.Fatalf("unexpected history: %+v", views)
	}
	one, two := views[0].Rounds[0], views[0].Rounds[1]
	if one.Findings != 2 || one.Clean || one.Applied != 1 {
		t.Fatalf("round 1 was read wrong: %+v", one)
	}
	if !one.HasDiff() {
		t.Fatalf("round 1 should have a diff: %+v", one)
	}
	if !two.Clean || two.HasDiff() {
		t.Fatalf("round 2 should be clean with no diff: %+v", two)
	}
	if views[0].Baseline() == "" || views[0].Last() == "" {
		t.Fatalf("run diff endpoints are missing: %+v", views[0])
	}
}

func TestBrowseSharesCheckpointLookupAcrossRunsInARepository(t *testing.T) {
	state := t.TempDir()
	repository := newRepository(t)
	log := Log{StateDir: state}
	for _, id := range []string{"2026-08-09T10-00-00Z", "2026-08-09T11-00-00Z"} {
		if err := log.WriteRun(RunRecord{ID: id, Repository: repository}); err != nil {
			t.Fatal(err)
		}
		archiveRound(t, state, id, 1, Review{Status: StatusClean}, Decisions{})
		checkpoints := Checkpoints{Repository: repository, RunID: id, Enabled: true}
		for _, round := range []int{0, 1} {
			writeRepoFile(t, repository, "tracked.txt", id+strconv.Itoa(round))
			if _, err := checkpoints.Save(t.Context(), round); err != nil {
				t.Fatal(err)
			}
		}
	}

	views := Browse(t.Context(), state)
	if len(views) != 2 {
		t.Fatalf("got %d runs, want 2", len(views))
	}
	for _, view := range views {
		if view.Baseline() == "" || view.Last() == "" {
			t.Fatalf("run %s is missing checkpoint endpoints: %+v", view.Record.ID, view)
		}
	}
}

func TestListRunsIsNewestFirst(t *testing.T) {
	state := t.TempDir()
	log := Log{StateDir: state}
	for _, id := range []string{"2026-08-09T10-00-00Z", "2026-08-09T12-00-00Z"} {
		if err := log.WriteRun(RunRecord{ID: id, Repository: "/repo"}); err != nil {
			t.Fatal(err)
		}
	}
	runs := ListRuns(state)
	if len(runs) != 2 || runs[0].ID != "2026-08-09T12-00-00Z" {
		t.Fatalf("runs are not newest first: %+v", runs)
	}
	if latest, ok := LatestRun(state, "/repo"); !ok || latest.ID != "2026-08-09T12-00-00Z" {
		t.Fatalf("latest run for the repository is wrong: %+v", latest)
	}
}

func TestNewRunIDSortsChronologically(t *testing.T) {
	base := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	// the fractional part is where a trimmed layout breaks: 1ns must sort before
	// 12ns, and an exact second before both.
	instants := []time.Time{base, base.Add(time.Nanosecond), base.Add(12 * time.Nanosecond), base.Add(time.Millisecond), base.Add(time.Second)}
	previous := ""
	for _, at := range instants {
		id := NewRunID(at)
		if id <= previous {
			t.Fatalf("run id %q does not sort after %q", id, previous)
		}
		if strings.ContainsAny(id, ":.") {
			t.Fatalf("run id %q is not usable as a path or ref component", id)
		}
		previous = id
	}
}
