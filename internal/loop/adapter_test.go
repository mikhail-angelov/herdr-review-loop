package loop

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func TestCodexCarriesTheLevelInItsInstructions(t *testing.T) {
	// codex's in-pane /review takes free-text instructions and has no level argument, so the
	// round's narrowing has to travel in that text
	native, delegated := Adapt("codex", nil, Policy([]config.Round{{Level: levelLow}}, 3), worktree, "/run/round-03/review.json", nil)
	if !delegated {
		t.Fatal("codex has a review command and should not fall back")
	}
	if !strings.HasPrefix(native.Command, "/review ") {
		t.Fatalf("command %q", native.Command)
	}
	if !strings.Contains(native.Command, "Closure pass") {
		t.Fatalf("the round's level did not reach the command line: %q", native.Command)
	}
	if strings.Contains(native.Command, "\n") {
		t.Fatalf("a slash command occupies one line: %q", native.Command)
	}
	if !strings.Contains(native.Capture, "/run/round-03/review.json") || !strings.Contains(native.Capture, `"open_questions"`) {
		t.Fatalf("capture step %q", native.Capture)
	}
}

// TestClaudeFallsBackToTheBuiltInPrompt records why that kind has no entry: its own review command
// fans out to subagents, and the findings then live outside the context of the session that ran it,
// which leaves the capture step nothing to serialize.
func TestClaudeFallsBackToTheBuiltInPrompt(t *testing.T) {
	rounds := []config.Round{{Level: levelMedium, Instructions: "only the public API surface"}}
	if _, delegated := Adapt("claude", nil, Policy(rounds, 1), worktree, "/review.json", nil); delegated {
		t.Fatal("claude was delegated to a review command of its own")
	}
}

func TestAnUnknownKindFallsBackToTheBuiltInPrompt(t *testing.T) {
	for _, kind := range []string{"gemini", "opencode", "something-new", ""} {
		if _, delegated := Adapt(kind, nil, Policy(nil, 1), worktree, "/review.json", nil); delegated {
			t.Errorf("kind %q was delegated, but the loop has no command for it", kind)
		}
	}
}

func TestReviewCommandOverridesAnyKindKnownOrNot(t *testing.T) {
	overrides := map[string]string{"gemini": "review", "claude": "/deep-review", "codex": ""}
	native, delegated := Adapt("gemini", overrides, Policy(nil, 1), worktree, "/review.json", nil)
	if !delegated || !strings.HasPrefix(native.Command, "/review ") {
		t.Fatalf("an entry for an unknown kind should make it supported: %#v %v", native, delegated)
	}
	// claude has no entry of its own, so an override is how a build of its review command that
	// reviews in-pane is put back in service
	native, delegated = Adapt("claude", overrides, Policy([]config.Round{{Level: levelLow}}, 1), worktree, "/review.json", nil)
	if !delegated || !strings.HasPrefix(native.Command, "/deep-review ") {
		t.Fatalf("an override should delegate the kind it names: %#v", native)
	}
	// an empty override turns the native path off and returns that kind to the built-in prompt
	if _, delegated = Adapt("codex", overrides, Policy(nil, 1), worktree, "/review.json", nil); delegated {
		t.Fatal("an empty override should return the kind to the built-in prompt")
	}
}

func TestARoundMayNameADifferentCommand(t *testing.T) {
	rounds := []config.Round{{Level: levelHigh, Command: "security-review"}}
	native, delegated := Adapt("claude", nil, Policy(rounds, 1), worktree, "/review.json", nil)
	if !delegated {
		t.Fatal("the round's command should delegate a kind with no table entry")
	}
	if !strings.HasPrefix(native.Command, "/security-review ") {
		t.Fatalf("command %q", native.Command)
	}
	if command := DescribeCommand("claude", nil, Policy(rounds, 1), worktree); command != "/security-review" {
		t.Fatalf("described command %q", command)
	}
}

// A native review command scopes itself, and the ones this table knows scope themselves to the
// uncommitted changes. Delegating a branch review to one would review the working tree and call it
// the branch, so those rounds take the built-in prompt — unless the round names a command itself,
// which is the user saying their command does something else.
func TestABranchReviewIsNotDelegatedToACommandThatScopesItself(t *testing.T) {
	branch := Scope{Spec: "branch:main", BaseRef: "main", Base: "abc123"}
	if _, delegated := Adapt("codex", nil, Policy(nil, 1), branch, "/review.json", nil); delegated {
		t.Fatal("a branch review was handed to a command that only sees uncommitted changes")
	}
	if command := DescribeCommand("codex", nil, Policy(nil, 1), branch); command != "built-in review prompt" {
		t.Fatalf("described %q, want the built-in prompt", command)
	}
	rounds := []config.Round{{Command: "/review --base main"}}
	native, delegated := Adapt("codex", nil, Policy(rounds, 1), branch, "/review.json", nil)
	if !delegated || !strings.HasPrefix(native.Command, "/review --base main") {
		t.Fatalf("a command the round named was refused: %#v %t", native, delegated)
	}
	if !strings.Contains(native.Command, "abc123") {
		t.Fatalf("the instructions do not name the base commit: %q", native.Command)
	}
}
