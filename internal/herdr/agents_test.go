package herdr

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func TestPickReviewerPrecedence(t *testing.T) {
	author := Agent{PaneID: "a", WorkspaceID: "w", Kind: "codex"}
	agents := []Agent{author, {PaneID: "same", WorkspaceID: "w", Kind: "codex", Name: "same"}, {PaneID: "claude", WorkspaceID: "w", Kind: "claude", Name: "reviewer"}, {PaneID: "other", WorkspaceID: "other", Kind: "claude"}}
	got, err := PickReviewer(config.Values{ReviewerName: "same", ReviewerKind: "claude"}, agents, author, nil)
	if err != nil || got.PaneID != "same" {
		t.Fatalf("got %#v %v", got, err)
	}
	got, err = PickReviewer(config.Values{}, agents, author, nil)
	if err != nil || got.PaneID != "claude" {
		t.Fatalf("got %#v %v", got, err)
	}
}
func TestPickReviewerExplainsNone(t *testing.T) {
	author := Agent{PaneID: "a", WorkspaceID: "w", Kind: "codex"}
	_, err := PickReviewer(config.Values{}, []Agent{author, {PaneID: "b", WorkspaceID: "w", Kind: "codex"}}, author, nil)
	if err == nil || !strings.Contains(err.Error(), "found codex @ b") {
		t.Fatalf("wrong error %v", err)
	}
}
