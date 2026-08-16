package ui

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func TestPanelViewClipsAndKeepsHints(t *testing.T) {
	for _, width := range []int{32, 44, 80} {
		view := PanelView(PanelState{Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Events: []string{"a very long log line that must be clipped"}}, width, 8)
		if !strings.Contains(view, "r review") || !strings.Contains(view, "q close") {
			t.Fatalf("width %d: unexpected view %q", width, view)
		}
		if !strings.Contains(view, "\x1b[0m") {
			t.Fatalf("width %d: header reset missing: %q", width, view)
		}
	}
}

func TestPanelViewShowsOneShotChoicesWhenItIsWaiting(t *testing.T) {
	view := PanelView(PanelState{Running: true, OneShotPending: true}, 80, 12)
	for _, hint := range []string{"a apply", "c cancel", "i instruction"} {
		if !strings.Contains(view, hint) {
			t.Fatalf("missing %q in %q", hint, view)
		}
	}
}

func TestPanelKeysSubmitAOneShotChoiceOrCustomInstruction(t *testing.T) {
	actions := PanelActions{
		Apply:  func() string { return "applied" },
		Cancel: func() string { return "canceled" },
		Custom: func(text string) string { return "custom: " + text },
	}
	state := PanelState{OneShotPending: true}
	message, custom, input, exit := panelKey("a", state, actions, "", false, "")
	if message != "applied" || custom || input != "" || exit {
		t.Fatalf("apply = %q %t %q %t", message, custom, input, exit)
	}
	message, custom, input, exit = panelKey("i", state, actions, "", false, "")
	if message != "" || !custom || input != "" || exit {
		t.Fatalf("instruction start = %q %t %q %t", message, custom, input, exit)
	}
	message, custom, input, exit = panelKey("x", state, actions, message, custom, input)
	if message != "" || !custom || input != "x" || exit {
		t.Fatalf("instruction input = %q %t %q %t", message, custom, input, exit)
	}
	message, custom, input, exit = panelKey("\n", state, actions, message, custom, input)
	if message != "custom: x" || custom || input != "" || exit {
		t.Fatalf("instruction submit = %q %t %q %t", message, custom, input, exit)
	}
}

func TestPanelViewFitsAvailableRowsWithoutWrapping(t *testing.T) {
	view := PanelView(PanelState{
		Events:  []string{"a log line that is deliberately much wider than the panel"},
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
	pane := &settingsPane{directory: "/tmp/config", fields: config.Fields(), values: values, message: "message"}
	view := pane.view("review loop running (pid 42, since 12:34)", 100)
	if !strings.Contains(view, "review loop running (pid 42, since 12:34)") {
		t.Fatalf("missing run status: %q", view)
	}
	if !strings.Contains(view, "\x1b[2m30m\x1b[0m") {
		t.Fatalf("default values are not dimmed: %q", view)
	}
	if strings.Contains(view, "\x1b[2m2\x1b[0m") {
		t.Fatalf("non-default value is dimmed: %q", view)
	}
	pane.message, pane.editing, pane.input = "enter accept", true, "claude"
	editing := pane.view("", 100)
	if !strings.Contains(editing, "reviewer kind") || !strings.Contains(editing, "claude") {
		t.Fatalf("missing edit state: %q", editing)
	}
}

func TestSettingsViewMatchesNodePopupLayout(t *testing.T) {
	pane := &settingsPane{directory: "/tmp/config", fields: config.Fields(), values: config.Defaults(), message: "saved"}
	view := pane.view("", 80)
	if !strings.HasPrefix(view, "\n  ") {
		t.Fatalf("settings should begin with the Node popup's padded header: %q", view)
	}
	if !strings.Contains(view, config.Fields()[0].Hint) || !strings.Contains(view, "j/k move · enter edit") {
		t.Fatalf("settings should keep the selected-field hint and key help visible: %q", view)
	}
}

func TestSettingsViewDoesNotRepeatKeyHelpInMessage(t *testing.T) {
	keys := "j/k move · enter edit · d default · s save · x cancel run · q close"
	pane := &settingsPane{directory: "/tmp/config", fields: config.Fields(), values: config.Defaults(), message: keys}
	view := pane.view("", 80)
	if got := strings.Count(view, keys); got != 1 {
		t.Fatalf("key help appears %d times, want once: %q", got, view)
	}
}
