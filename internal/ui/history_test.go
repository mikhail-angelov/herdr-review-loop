package ui

import (
	"strings"
	"testing"
)

func sampleRuns() []HistoryRun {
	return []HistoryRun{
		{Title: "2026-08-09 19:12 · claude @ w:p1 ← codex @ w:p2 · 2 round(s) · clean", Rounds: []HistoryRound{
			{Label: "round 1   6 finding(s)", HasDiff: true},
			{Label: "round 2   clean"},
		}},
		{Title: "2026-08-09 14:40 · claude @ w:p1 ← codex @ w:p2 · 1 round(s) · canceled"},
	}
}

func TestHistoryViewListsRunsAndOpensRounds(t *testing.T) {
	closed := HistoryView(sampleRuns(), 0, 0, false, "", 100, 20)
	if !strings.Contains(closed, "2026-08-09 19:12") || !strings.Contains(closed, "2026-08-09 14:40") {
		t.Fatalf("runs are missing: %q", closed)
	}
	if strings.Contains(closed, "round 1") {
		t.Fatalf("rounds are shown before the run is opened: %q", closed)
	}
	opened := HistoryView(sampleRuns(), 0, 1, true, "", 100, 20)
	if !strings.Contains(opened, "round 1") || !strings.Contains(opened, "round 2") {
		t.Fatalf("rounds are missing: %q", opened)
	}
	if !strings.Contains(opened, "d round diff") {
		t.Fatalf("open-run keys are missing: %q", opened)
	}
}

func TestHistoryViewMarksRoundsWithoutADiff(t *testing.T) {
	view := HistoryView(sampleRuns(), 0, 0, true, "", 100, 20)
	lines := strings.Split(view, "\n")
	var first, second string
	for _, line := range lines {
		if strings.Contains(line, "round 1") {
			first = line
		}
		if strings.Contains(line, "round 2") {
			second = line
		}
	}
	if !strings.HasSuffix(first, "diff") {
		t.Fatalf("a checkpointed round is not marked: %q", first)
	}
	if !strings.HasSuffix(strings.TrimSuffix(second, "\x1b[0m"), "—") {
		t.Fatalf("a round without a checkpoint is not marked: %q", second)
	}
}

func TestHistoryViewReportsAnEmptyHistory(t *testing.T) {
	view := HistoryView(nil, 0, 0, false, "", 80, 10)
	if !strings.Contains(view, "no recorded runs yet") {
		t.Fatalf("unexpected empty view: %q", view)
	}
}

func TestHistorySkipsEnterDiffAndRestoreForARunWithoutRounds(t *testing.T) {
	runs := []HistoryRun{{Title: "canceled before round one"}}
	for _, key := range []string{"enter", "d", "c"} {
		called := false
		if performRoundAction(runs, 0, 0, true, func() { called = true }) {
			t.Fatalf("%s invoked an action for an empty run", key)
		}
		if called {
			t.Fatalf("%s callback was invoked for an empty run", key)
		}
	}
}

func TestHistoryViewFitsTheAvailableRows(t *testing.T) {
	view := HistoryView(sampleRuns(), 0, 0, true, "a message", 40, 6)
	if got := len(strings.Split(view, "\n")); got > 6 {
		t.Fatalf("history uses %d rows, want at most 6", got)
	}
	for _, line := range strings.Split(view, "\n") {
		if len([]rune(stripANSI(line))) > 40 {
			t.Fatalf("line exceeds the pane width: %q", line)
		}
	}
}

func TestHistoryViewKeepsTheSelectedRunVisibleInAShortPane(t *testing.T) {
	runs := make([]HistoryRun, 12)
	for index := range runs {
		runs[index].Title = "run " + string(rune('A'+index))
	}
	view := HistoryView(runs, 10, 0, false, "", 80, 6)
	if !strings.Contains(view, "> run K") {
		t.Fatalf("selected run is outside the viewport: %q", view)
	}
	if strings.Contains(view, "run A") {
		t.Fatalf("viewport still starts with the oldest run: %q", view)
	}
	if got := len(strings.Split(view, "\n")); got > 6 {
		t.Fatalf("history uses %d rows, want at most 6", got)
	}
}

// stripANSI removes styling so width assertions measure what is displayed
// rather than the escape sequences around it.
func stripANSI(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '\x1b' {
			for index < len(value) && value[index] != 'm' {
				index++
			}
			continue
		}
		out.WriteByte(value[index])
	}
	return out.String()
}
