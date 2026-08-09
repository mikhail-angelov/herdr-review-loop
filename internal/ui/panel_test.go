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
