package loop

import (
	"strings"
	"testing"
	"time"
)

func TestLogTailStaysWithinItsWindow(t *testing.T) {
	log := Log{StateDir: t.TempDir()}
	for range 2000 {
		if err := log.Write(strings.Repeat("x", 10)); err != nil {
			t.Fatal(err)
		}
	}
	tail, err := log.Tail()
	if err != nil || len(tail) > 8192 {
		t.Fatalf("tail=%d err=%v", len(tail), err)
	}
}

func TestLatestFeedReadsThePhaseAndEventsOfTheNewestRun(t *testing.T) {
	state := t.TempDir()
	log := Log{StateDir: state}
	for _, id := range []string{"2026-08-09T10-00-00Z", "2026-08-09T12-00-00Z"} {
		if err := log.WriteRun(RunRecord{ID: id, Repository: "/repo", Outcome: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	archive := openArchive(t, state, "2026-08-09T12-00-00Z")
	for _, event := range []struct {
		round               int
		phase, name, detail string
	}{
		{1, PhaseReview, EventPhaseStart, ""},
		{1, PhaseReview, EventPhaseDone, "2 finding(s)"},
		{1, PhaseFix, EventPhaseStart, ""},
		{1, PhaseFix, EventStall, "no output 5m12s"},
	} {
		if err := archive.Event(event.round, event.phase, event.name, event.detail); err != nil {
			t.Fatal(err)
		}
	}
	feed := LatestFeed(state, 10)
	if feed.Phase != "round 1 fix" {
		t.Fatalf("phase %q", feed.Phase)
	}
	if feed.Outcome != "" {
		t.Fatalf("a running run reported an outcome: %q", feed.Outcome)
	}
	if len(feed.Lines) != 4 || !strings.Contains(feed.Lines[3], "no output 5m12s") {
		t.Fatalf("lines %#v", feed.Lines)
	}
}

func TestLatestFeedReportsTheOutcomeOfAFinishedRun(t *testing.T) {
	state := t.TempDir()
	if err := (Log{StateDir: state}).WriteRun(RunRecord{ID: "2026-08-09T10-00-00Z", Outcome: "clean after 2 round(s)"}); err != nil {
		t.Fatal(err)
	}
	if got := LatestFeed(state, 10).Outcome; got != "clean after 2 round(s)" {
		t.Fatalf("outcome %q", got)
	}
}

func TestFormatEventKeepsTheTimestampAsAStrippablePrefix(t *testing.T) {
	line := FormatEvent(Event{TS: time.Now(), Round: 2, Phase: PhaseReview, Event: EventRetry, Detail: "parse failed"})
	if !strings.HasPrefix(line, "[") || !strings.Contains(line, "] round 2 review: retry — parse failed") {
		t.Fatalf("line %q", line)
	}
}

func TestLatestFeedIsEmptyWithoutRuns(t *testing.T) {
	if feed := LatestFeed(t.TempDir(), 10); feed.Phase != "" || feed.Outcome != "" || len(feed.Lines) != 0 {
		t.Fatalf("feed %+v", feed)
	}
}
