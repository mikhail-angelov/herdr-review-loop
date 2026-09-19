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
	feed := LatestFeed(state, "/repo", 10)
	if feed.Phase != "round 1 fix" {
		t.Fatalf("phase %q", feed.Phase)
	}
	if feed.Outcome != "" {
		t.Fatalf("a running run reported an outcome: %q", feed.Outcome)
	}
	if len(feed.Lines) != 4 || !strings.Contains(feed.Lines[3].Text, "no output 5m12s") || feed.Lines[3].Event != EventStall {
		t.Fatalf("lines %#v", feed.Lines)
	}
}

func TestLatestFeedReportsTheOutcomeOfAFinishedRun(t *testing.T) {
	state := t.TempDir()
	if err := (Log{StateDir: state}).WriteRun(RunRecord{ID: "2026-08-09T10-00-00Z", Repository: "/repo", Outcome: "clean after 2 round(s)"}); err != nil {
		t.Fatal(err)
	}
	if got := LatestFeed(state, "/repo", 10).Outcome; got != "clean after 2 round(s)" {
		t.Fatalf("outcome %q", got)
	}
}

// A workspace that has never been reviewed must show nothing at all. The state directory is shared
// by every workspace the plugin runs in, so the alternative is a panel showing another space's run
// as if it were this one's.
func TestLatestFeedShowsNothingForARepositoryThatWasNeverReviewed(t *testing.T) {
	state := t.TempDir()
	if err := (Log{StateDir: state}).WriteRun(RunRecord{ID: "2026-08-09T10-00-00Z", Repository: "/elsewhere", Outcome: "clean after 2 round(s)"}); err != nil {
		t.Fatal(err)
	}
	if feed := LatestFeed(state, "/repo", 10); feed.Outcome != "" || feed.Phase != "" || len(feed.Lines) != 0 {
		t.Fatalf("another repository's run leaked into this panel: %#v", feed)
	}
}

func TestFormatEventKeepsTheTimestampAsAStrippablePrefix(t *testing.T) {
	line := FormatEvent(Event{TS: time.Now(), Round: 2, Phase: PhaseReview, Event: EventRetry, Detail: "parse failed"})
	if !strings.HasPrefix(line, "[") || !strings.Contains(line, "] round 2 review: retry — parse failed") {
		t.Fatalf("line %q", line)
	}
}

func TestLatestFeedIsEmptyWithoutRuns(t *testing.T) {
	if feed := LatestFeed(t.TempDir(), "/repo", 10); feed.Phase != "" || feed.Outcome != "" || len(feed.Lines) != 0 {
		t.Fatalf("feed %+v", feed)
	}
}

func TestLogDocumentCarriesTheDetailThePanelHasToClip(t *testing.T) {
	state := t.TempDir()
	log := Log{StateDir: state}
	if err := log.WriteRun(RunRecord{ID: "2026-08-09T12-00-00Z", Repository: "/repo", Rounds: 1, Outcome: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := log.Write("review unreadable, asking for a reformat"); err != nil {
		t.Fatal(err)
	}
	archive := openArchive(t, state, "2026-08-09T12-00-00Z")
	detail := "no output from gemini @ reviewer (w1:p2) for 5m0s, which is a very long line the panel cannot hold"
	if err := archive.Event(1, PhaseReview, EventStall, detail); err != nil {
		t.Fatal(err)
	}
	document := LogDocument(state, "/repo", MarkdownStyle())
	for _, want := range []string{detail, "review unreadable", "/repo", "events", "journal"} {
		if !strings.Contains(document, want) {
			t.Fatalf("the log document is missing %q:\n%s", want, document)
		}
	}
	if got := LogDocument(state, "/elsewhere", MarkdownStyle()); strings.Contains(got, detail) {
		t.Fatalf("another repository's run leaked into the log: %s", got)
	}
}

// The log pane and the findings pane are read side by side, so the log takes its decoration from
// the same Style — and an event is decorated by which event it was, not by where it sits.
func TestLogDocumentDecoratesEachEventByItsOwnName(t *testing.T) {
	state := t.TempDir()
	log := Log{StateDir: state}
	if err := log.WriteRun(RunRecord{ID: "2026-08-09T12-00-00Z", Repository: "/repo", Rounds: 1, Outcome: "clean"}); err != nil {
		t.Fatal(err)
	}
	archive := openArchive(t, state, "2026-08-09T12-00-00Z")
	for _, event := range []string{EventPhaseStart, EventStall, EventPhaseDone} {
		if err := archive.Event(1, PhaseReview, event, ""); err != nil {
			t.Fatal(err)
		}
	}
	style := MarkdownStyle()
	style.Event = func(event, text string) string { return "<" + event + ">" + text }
	document := LogDocument(state, "/repo", style)
	for _, want := range []string{"<" + EventPhaseStart + ">", "<" + EventStall + ">", "<" + EventPhaseDone + ">"} {
		if !strings.Contains(document, want) {
			t.Fatalf("the log document is missing %q:\n%s", want, document)
		}
	}
	// the headings and the journal are the document's own, never an event's
	if strings.Contains(document, "<>") {
		t.Fatalf("a line that is not an event was decorated as one:\n%s", document)
	}
}
