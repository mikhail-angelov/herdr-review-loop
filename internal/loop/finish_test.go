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
	if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFinishArchivesDecisionsBeforeRemovingThem(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	record := RunRecord{ID: "2026-08-09T10-00-00Z", Repository: repository, Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Rounds: 2, Outcome: "clean", Started: time.Now().UTC()}
	if err := (Log{StateDir: state}).WriteRun(record); err != nil {
		t.Fatal(err)
	}
	decisions := "- applied: fixed the leak\n- rejected: too broad\n- deferred: needs a design\n- rejected: not a bug\ntests: go test ./... passed\n"
	writeRepoFile(t, repository, "review.md", "STATUS: CLEAN\n")
	writeRepoFile(t, repository, SummaryFile, decisions)

	result, err := Finish(state, "workspace", repository, "review.md")
	if err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(RunDir(state, record.ID), summaryArchive))
	if err != nil {
		t.Fatalf("decisions were not archived: %v", err)
	}
	if string(archived) != decisions {
		t.Fatalf("archived decisions differ: %q", archived)
	}
	for _, name := range []string{"review.md", SummaryFile} {
		if _, err := os.Lstat(filepath.Join(repository, name)); !os.IsNotExist(err) {
			t.Fatalf("%s survived finish: %v", name, err)
		}
	}
	if result.Stats.Applied != 1 || result.Stats.Rejected != 2 || result.Stats.Deferred != 1 {
		t.Fatalf("unexpected counts: %+v", result.Stats)
	}
	digest := strings.Join(result.Digest(), "\n")
	for _, want := range []string{"2 round(s)", "clean", "1 applied · 2 rejected · 1 deferred", "tests: go test ./... passed", "removed review.md, " + SummaryFile} {
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
	writeRepoFile(t, repository, "review.md", "STATUS: FINDINGS\n")

	if _, err := Finish(state, "workspace", repository, "review.md"); err == nil {
		t.Fatal("finish deleted files underneath a running loop")
	}
	if _, err := os.Lstat(filepath.Join(repository, "review.md")); err != nil {
		t.Fatalf("review file was removed despite the refusal: %v", err)
	}
}

func TestFinishKeepsDecisionsWhenNoRunRecordExists(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	writeRepoFile(t, repository, SummaryFile, "- applied: something\n")

	result, err := Finish(state, "workspace", repository, "review.md")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Archived || result.Run.ID == "" {
		t.Fatalf("decisions were dropped instead of archived: %+v", result)
	}
	if _, err := os.ReadFile(filepath.Join(RunDir(state, result.Run.ID), summaryArchive)); err != nil {
		t.Fatalf("orphan decisions were not archived: %v", err)
	}
}

func TestFinishReportsTheRunWhenThereAreNoDecisions(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	// a run that came back clean on its first round: one review, no author phase.
	record := RunRecord{ID: "2026-08-09T10-00-00-000000000Z", Repository: repository, Author: "claude", Reviewer: "codex", Rounds: 1, Outcome: "clean", Started: time.Now().UTC()}
	if err := (Log{StateDir: state}).WriteRun(record); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repository, "review.md", "STATUS: CLEAN\n")

	result, err := Finish(state, "workspace", repository, "review.md")
	if err != nil {
		t.Fatal(err)
	}
	if result.Run.ID != record.ID {
		t.Fatalf("finish did not report the run it cleaned up after: %+v", result.Run)
	}
	digest := strings.Join(result.Digest(), "\n")
	for _, want := range []string{"1 round(s)", "clean", "removed review.md"} {
		if !strings.Contains(digest, want) {
			t.Fatalf("digest missing %q: %s", want, digest)
		}
	}
}

func TestFinishReportsNothingToDoOnACleanTree(t *testing.T) {
	state, repository := t.TempDir(), t.TempDir()
	result, err := Finish(state, "workspace", repository, "review.md")
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

func TestParseSummaryIgnoresAnUnmarkedRecord(t *testing.T) {
	stats := ParseSummary("some prose\nanother line\n")
	if stats.Decisions() != 0 || stats.Tests != "" {
		t.Fatalf("unmarked record produced counts: %+v", stats)
	}
}

func TestBrowsePairsVerdictsWithTheCheckpointsThatFollowThem(t *testing.T) {
	state := t.TempDir()
	repository := newRepository(t)
	log := Log{StateDir: state}
	record := RunRecord{ID: "2026-08-09T10-00-00Z", Repository: repository, Author: "claude", Reviewer: "codex", Rounds: 2, Outcome: "clean"}
	if err := log.WriteRun(record); err != nil {
		t.Fatal(err)
	}
	if err := log.Archive(record.ID, 1, "STATUS: FINDINGS\n- [high] a.go:1 — wrong — fix it\n- [low] b.go:2 — nit — maybe\n"); err != nil {
		t.Fatal(err)
	}
	if err := log.Archive(record.ID, 2, "STATUS: CLEAN\n"); err != nil {
		t.Fatal(err)
	}
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
	first, second := views[0].Rounds[0], views[0].Rounds[1]
	if first.Findings != 2 || first.Clean {
		t.Fatalf("round 1 was read wrong: %+v", first)
	}
	if !first.HasDiff() {
		t.Fatalf("round 1 should have a diff: %+v", first)
	}
	if !second.Clean || second.HasDiff() {
		t.Fatalf("round 2 should be clean with no diff: %+v", second)
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
		record := RunRecord{ID: id, Repository: repository}
		if err := log.WriteRun(record); err != nil {
			t.Fatal(err)
		}
		if err := log.Archive(id, 1, "STATUS: CLEAN\n"); err != nil {
			t.Fatal(err)
		}
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
