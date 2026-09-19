package loop

import "testing"

func TestPendingRequestIsScopedToItsWorkspaceAndRepository(t *testing.T) {
	state := t.TempDir()
	pending := Pending{ID: requestID("run", 1, PendingOneShot), Workspace: "workspace", Repository: "/repo", Run: "run", Round: 1, Kind: PendingOneShot}
	if err := publishPending(state, pending); err != nil {
		t.Fatal(err)
	}
	for _, other := range []struct{ workspace, repository string }{{"other", "/repo"}, {"workspace", "/elsewhere"}} {
		if _, ok := PendingRequest(state, other.workspace, other.repository); ok {
			t.Fatalf("%s %s can see the pending request", other.workspace, other.repository)
		}
	}
	if _, ok := PendingRequest(state, "workspace", "/repo"); !ok {
		t.Fatal("the owning panel cannot see the pending request")
	}
}

func TestAnswerRejectsActionsTheBoundaryDoesNotOffer(t *testing.T) {
	state := t.TempDir()
	gate := Pending{ID: requestID("run", 2, PendingGateReview), Workspace: "workspace", Repository: "/repo", Kind: PendingGateReview}
	if err := Answer(state, gate, ChoiceApply, ""); err == nil {
		t.Fatal("a gate accepted a one-shot action")
	}
	if err := Answer(state, gate, ChoiceInstruct, "  "); err == nil {
		t.Fatal("an empty instruction was accepted")
	}
	if err := Answer(state, gate, ChoiceContinue, ""); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerClearsThePauseThatProducedTheBoundary(t *testing.T) {
	state := t.TempDir()
	if err := SetPause(state, "workspace", "/repo", true); err != nil {
		t.Fatal(err)
	}
	if !Paused(state, "workspace", "/repo") {
		t.Fatal("the pause was not recorded")
	}
	gate := Pending{ID: requestID("run", 1, PendingGateRound), Workspace: "workspace", Repository: "/repo", Kind: PendingGateRound}
	if err := Answer(state, gate, ChoiceContinue, ""); err != nil {
		t.Fatal(err)
	}
	if Paused(state, "workspace", "/repo") {
		t.Fatal("continuing left the pause in place, so the loop would stop again at once")
	}
}

func TestAwaitChoiceIgnoresAnAnswerToAnotherBoundary(t *testing.T) {
	state := t.TempDir()
	earlier := Pending{ID: requestID("run", 1, PendingGateRound), Workspace: "workspace", Repository: "/repo", Kind: PendingGateRound}
	if err := Answer(state, earlier, ChoiceContinue, ""); err != nil {
		t.Fatal(err)
	}
	current := Pending{ID: requestID("run", 2, PendingGateRound), Workspace: "workspace", Repository: "/repo", Kind: PendingGateRound}
	control := readControl(state, "workspace", "/repo")
	if control.Choice.Request == current.ID {
		t.Fatal("the stale answer names the current request")
	}
	if err := Answer(state, current, ChoiceStop, ""); err != nil {
		t.Fatal(err)
	}
	choice, err := awaitChoice(t.Context(), state, current)
	if err != nil {
		t.Fatal(err)
	}
	if choice.Action != ChoiceStop {
		t.Fatalf("got %q, want %q", choice.Action, ChoiceStop)
	}
}
