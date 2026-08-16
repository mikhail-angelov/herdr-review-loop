package loop

import "testing"

func TestOneShotChoiceIsScopedToItsPanelAndOnlyAcceptedOnce(t *testing.T) {
	state := t.TempDir()
	pending := OneShotPending{Workspace: "workspace", Repository: "/repo", Run: "run", Round: 1}
	if err := CreateOneShotPending(state, pending); err != nil {
		t.Fatal(err)
	}
	if _, ok := PendingOneShot(state, "other", "/repo"); ok {
		t.Fatal("another workspace can see the pending review")
	}
	if err := ChooseOneShot(state, "workspace", "/repo", OneShotCustom, "run focused tests"); err != nil {
		t.Fatal(err)
	}
	if err := ChooseOneShot(state, "workspace", "/repo", OneShotApply, ""); err == nil {
		t.Fatal("a second choice replaced the first")
	}
}
