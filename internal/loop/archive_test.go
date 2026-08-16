package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveKeepsPromptsOutputAndTheReportOfEachRound(t *testing.T) {
	state := t.TempDir()
	archive := openArchive(t, state, "run")
	for _, write := range []func() error{
		func() error { return archive.Prompt(1, PhaseReview, "what the reviewer was sent") },
		func() error { return archive.Raw(1, PhaseReview, "what the reviewer answered") },
		func() error { return archive.Prompt(1, PhaseFix, "what the author was sent") },
		func() error { return archive.Raw(1, PhaseFix, "what the author answered") },
		func() error { return archive.Patch(1, "diff --git a/a.go b/a.go\n") },
	} {
		if err := write(); err != nil {
			t.Fatal(err)
		}
	}
	round := filepath.Join(ArchiveDir(state, "run"), "round-01")
	for name, want := range map[string]string{
		promptReview: "what the reviewer was sent",
		rawReview:    "what the reviewer answered",
		promptFix:    "what the author was sent",
		rawFix:       "what the author answered",
		changesPatch: "diff --git a/a.go b/a.go\n",
	} {
		data, err := os.ReadFile(filepath.Join(round, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q %v, want %q", name, data, err, want)
		}
	}
}

func TestArchiveWithoutRawOutputKeepsEverythingElse(t *testing.T) {
	state := t.TempDir()
	archive, err := OpenArchive(state, "run", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Raw(1, PhaseReview, "verbatim model output"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Prompt(1, PhaseReview, "the prompt"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Event(1, PhaseReview, EventDegraded, "parse"); err != nil {
		t.Fatal(err)
	}
	round := filepath.Join(ArchiveDir(state, "run"), "round-01")
	if _, err := os.Stat(filepath.Join(round, rawReview)); !os.IsNotExist(err) {
		t.Fatalf("raw output was retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(round, promptReview)); err != nil {
		t.Fatalf("the prompt was dropped along with the raw output: %v", err)
	}
	if events := ReadEvents(ArchiveDir(state, "run")); len(events) != 1 || events[0].Event != EventDegraded {
		t.Fatalf("the failure was not recorded: %#v", events)
	}
}

func TestReadEventsReturnsTheStreamInOrder(t *testing.T) {
	state := t.TempDir()
	archive := openArchive(t, state, "run")
	for _, name := range []string{EventPhaseStart, EventStall, EventRetry, EventPhaseDone} {
		if err := archive.Event(2, PhaseFix, name, name+" detail"); err != nil {
			t.Fatal(err)
		}
	}
	events := ReadEvents(ArchiveDir(state, "run"))
	if len(events) != 4 || events[0].Event != EventPhaseStart || events[3].Event != EventPhaseDone {
		t.Fatalf("events %#v", events)
	}
	if events[1].Round != 2 || events[1].Phase != PhaseFix || events[1].TS.IsZero() {
		t.Fatalf("second event %#v", events[1])
	}
}

func TestReadEventsIsEmptyForARunThatWroteNone(t *testing.T) {
	if events := ReadEvents(ArchiveDir(t.TempDir(), "missing")); len(events) != 0 {
		t.Fatalf("events %#v", events)
	}
}

func TestArchivedRoundsAreInOrder(t *testing.T) {
	state := t.TempDir()
	archive := openArchive(t, state, "run")
	for _, round := range []int{3, 1, 12, 2} {
		if err := archive.Parsed(round, reviewFile, Review{Status: StatusClean}); err != nil {
			t.Fatal(err)
		}
	}
	got := ArchivedRounds(state, "run")
	want := []int{1, 2, 3, 12}
	if len(got) != len(want) {
		t.Fatalf("rounds %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("rounds %#v, want %#v", got, want)
		}
	}
}

func TestRotateDropsTheOldestRunsWhole(t *testing.T) {
	state := t.TempDir()
	log := Log{StateDir: state}
	ids := []string{"2026-08-09T10-00-00Z", "2026-08-09T11-00-00Z", "2026-08-09T12-00-00Z"}
	for _, id := range ids {
		if err := log.WriteRun(RunRecord{ID: id, Repository: "/repo"}); err != nil {
			t.Fatal(err)
		}
		archiveRound(t, state, id, 1, Review{Status: StatusClean}, Decisions{})
	}
	if err := Rotate(state, 2); err != nil {
		t.Fatal(err)
	}
	runs := ListRuns(state)
	if len(runs) != 2 || runs[0].ID != ids[2] || runs[1].ID != ids[1] {
		t.Fatalf("runs after rotation: %+v", runs)
	}
	if _, err := os.Stat(ArchiveDir(state, ids[0])); !os.IsNotExist(err) {
		t.Fatalf("the oldest run left something behind: %v", err)
	}
}

func TestRotateIsHarmlessWithoutAHistoryDirectory(t *testing.T) {
	if err := Rotate(t.TempDir(), 5); err != nil {
		t.Fatalf("got %v, want no error", err)
	}
}

func TestManifestAndReportRoundTrip(t *testing.T) {
	state := t.TempDir()
	archive := openArchive(t, state, "run")
	manifest := Manifest{Run: "run", Plugin: "0.1.0", Started: time.Now().UTC(), Scope: scopeWorktree, Rounds: []ManifestRound{{Round: 1, Command: "built-in review prompt", Level: "broad"}}}
	if err := archive.WriteManifest(manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(ArchiveDir(state, "run"), manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"plugin_version": "0.1.0"`) {
		t.Fatalf("manifest %s", data)
	}
	if err := archive.WriteReport(Report{Run: "run", ExitCode: ExitFindings, Outcome: "budget spent"}); err != nil {
		t.Fatal(err)
	}
	report, found := ReadReport(state, "run")
	if !found || report.ExitCode != ExitFindings || report.Outcome != "budget spent" {
		t.Fatalf("report %+v found=%v", report, found)
	}
}
