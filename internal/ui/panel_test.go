package ui

import (
	"strings"
	"testing"
)

func TestPanelViewClipsAndKeepsHints(t *testing.T) {
	view := PanelView(PanelState{Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Tail: "a very long log line that must be clipped"}, 32)
	if !strings.Contains(view, "r review") || !strings.Contains(view, "…") {
		t.Fatalf("unexpected view %q", view)
	}
}
