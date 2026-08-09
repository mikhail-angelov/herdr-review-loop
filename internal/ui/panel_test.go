package ui

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func TestPanelViewClipsAndKeepsHints(t *testing.T) {
	view := PanelView(PanelState{Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Tail: "a very long log line that must be clipped"}, 32, 8)
	if !strings.Contains(view, "r review") || !strings.Contains(view, "q close") {
		t.Fatalf("unexpected view %q", view)
	}
	if !strings.Contains(view, "\x1b[0m") {
		t.Fatalf("header reset missing: %q", view)
	}
}

func TestSettingsViewShowsRunStatusAndDimsDefaults(t *testing.T) {
	values := config.Defaults()
	values.MaxIterations = 2
	view := settingsView("/tmp/config", values, 0, "message", "review loop running (pid 42, since 12:34)", 100)
	if !strings.Contains(view, "review loop running (pid 42, since 12:34)") {
		t.Fatalf("missing run status: %q", view)
	}
	if !strings.Contains(view, "\x1b[2m30m\x1b[0m") {
		t.Fatalf("default values are not dimmed: %q", view)
	}
	if strings.Contains(view, "\x1b[2m2\x1b[0m") {
		t.Fatalf("non-default value is dimmed: %q", view)
	}
}
