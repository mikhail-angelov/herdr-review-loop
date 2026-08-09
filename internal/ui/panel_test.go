package ui

import (
	"strings"
	"testing"
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
