package ui

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func TestPanelViewClipsAndKeepsHints(t *testing.T) {
	for _, width := range []int{32, 44, 80} {
		view := PanelView(PanelState{Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Tail: "a very long log line that must be clipped"}, width, 8)
		if !strings.Contains(view, "r review") || !strings.Contains(view, "q close") {
			t.Fatalf("width %d: unexpected view %q", width, view)
		}
		if !strings.Contains(view, "\x1b[0m") {
			t.Fatalf("width %d: header reset missing: %q", width, view)
		}
	}
}

func TestPanelViewFitsAvailableRowsWithoutWrapping(t *testing.T) {
	view := PanelView(PanelState{
		Tail:    "a log line that is deliberately much wider than the panel",
		Message: "a status message that is deliberately much wider than the panel",
	}, 32, 8)
	if got := len(strings.Split(view, "\n")); got > 8 {
		t.Fatalf("panel uses %d rows, want at most 8: %q", got, view)
	}
	if strings.Contains(view, "\nwide than the panel") {
		t.Fatalf("panel wrapped a long line: %q", view)
	}
}

func TestSettingsViewShowsRunStatusAndDimsDefaults(t *testing.T) {
	values := config.Defaults()
	values.MaxIterations = 2
	view := settingsView("/tmp/config", values, 0, "message", "review loop running (pid 42, since 12:34)", 100, false, "")
	if !strings.Contains(view, "review loop running (pid 42, since 12:34)") {
		t.Fatalf("missing run status: %q", view)
	}
	if !strings.Contains(view, "\x1b[2m30m\x1b[0m") {
		t.Fatalf("default values are not dimmed: %q", view)
	}
	if strings.Contains(view, "\x1b[2m2\x1b[0m") {
		t.Fatalf("non-default value is dimmed: %q", view)
	}
	editing := settingsView("/tmp/config", values, 0, "enter accept", "", 100, true, "claude")
	if !strings.Contains(editing, "reviewer kind") || !strings.Contains(editing, "claude") {
		t.Fatalf("missing edit state: %q", editing)
	}
}

func TestSettingsViewMatchesNodePopupLayout(t *testing.T) {
	view := settingsView("/tmp/config", config.Defaults(), 0, "saved", "", 80, false, "")
	if !strings.HasPrefix(view, "\n  ") {
		t.Fatalf("settings should begin with the Node popup's padded header: %q", view)
	}
	if !strings.Contains(view, "agent kind that reviews") || !strings.Contains(view, "j/k move · enter edit") {
		t.Fatalf("settings should keep the selected-field hint and key help visible: %q", view)
	}
}

func TestSettingsViewDoesNotRepeatKeyHelpInMessage(t *testing.T) {
	keys := "j/k move · enter edit · d default · s save · x cancel run · q close"
	view := settingsView("/tmp/config", config.Defaults(), 0, keys, "", 80, false, "")
	if got := strings.Count(view, keys); got != 1 {
		t.Fatalf("key help appears %d times, want once: %q", got, view)
	}
}
