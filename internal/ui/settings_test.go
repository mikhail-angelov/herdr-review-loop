package ui

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func newSettingsPane() *settingsPane {
	values := config.Defaults()
	return &settingsPane{fields: config.Fields(), values: values, original: values}
}

// A pane shorter than the list it holds has to scroll it. Without this the terminal scrolls
// instead, taking the title and the first settings off the top of the screen while the selection
// moves invisibly.
func TestSettingsListScrollsToKeepTheSelectionVisible(t *testing.T) {
	pane := newSettingsPane()
	const rows = 14
	visible := func() string { return pane.view("", 80, rows) }
	if lines := strings.Count(visible(), "\n") + 1; lines > rows {
		t.Fatalf("the view is %d lines tall in a %d-row pane", lines, rows)
	}
	first := pane.fields[0]
	if !strings.Contains(visible(), first.Label) {
		t.Fatalf("the first setting is not shown:\n%s", visible())
	}
	for range len(pane.fields) - 1 {
		pane.command("j")
		last := pane.fields[pane.selected]
		if !strings.Contains(visible(), last.Label) {
			t.Fatalf("selecting %q scrolled it out of view:\n%s", last.Label, visible())
		}
	}
	if !strings.Contains(visible(), "↑") {
		t.Fatalf("a scrolled list does not say what is above it:\n%s", visible())
	}
	for range len(pane.fields) - 1 {
		pane.command("k")
	}
	if !strings.Contains(visible(), pane.fields[0].Label) || !strings.Contains(visible(), "↓") {
		t.Fatalf("moving back up did not scroll back:\n%s", visible())
	}
}

func TestSettingsShowsEverySettingWhenThePaneIsTallEnough(t *testing.T) {
	pane := newSettingsPane()
	view := pane.view("", 80, len(pane.fields)+settingsChrome)
	for _, field := range pane.fields {
		if !strings.Contains(view, field.Label) {
			t.Fatalf("a tall pane hid %q:\n%s", field.Label, view)
		}
	}
	if strings.Contains(view, "↑") || strings.Contains(view, "↓") {
		t.Fatalf("a complete list claimed to be scrolled:\n%s", view)
	}
}
