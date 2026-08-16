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
	if native.Preamble != "" {
		t.Fatalf("codex needs no preamble; its argument is free text: %q", native.Preamble)
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

func TestClaudeTakesTheLevelAsItsArgumentAndTheInstructionsFirst(t *testing.T) {
	rounds := []config.Round{{Level: levelMedium, Instructions: "only the public API surface"}}
	settled := []Settled{{Round: 1, Action: ActionRejected, Title: "context leak", Note: "canceled in the defer above"}}
	native, delegated := Adapt("claude", nil, Policy(rounds, 1), worktree, "/review.json", settled)
	if !delegated {
		t.Fatal("claude has a review command and should not fall back")
	}
	if native.Command != "/code-review medium" {
		t.Fatalf("command %q, want the level as the command's own argument", native.Command)
	}
	for _, want := range []string{"only the public API surface", "already settled", "canceled in the defer above", "git diff"} {
		if !strings.Contains(native.Preamble, want) {
			t.Fatalf("the preamble is missing %q:\n%s", want, native.Preamble)
		}
	}
}

func TestAnUnknownLevelLandsInTheMiddleOfTheCommandsOwnScale(t *testing.T) {
	for level, want := range map[string]string{
		levelLow: "low", levelMedium: "medium", levelHigh: "high", levelMax: "max",
		"ultra": "high", "": "high", "thorough": "high",
	} {
		native, _ := Adapt("claude", nil, Policy([]config.Round{{Level: level}}, 1), worktree, "/review.json", nil)
		if native.Command != "/code-review "+want {
			t.Errorf("level %q produced %q, want /code-review %s", level, native.Command, want)
		}
	}
}

// TestTheLoopNeverAsksForAMultiAgentReview guards §2.2: fan-out is out, and claude's own ultra is
// billed and user-triggered, so the loop must never reach it however a round is configured.
func TestTheLoopNeverAsksForAMultiAgentReview(t *testing.T) {
	for _, level := range []string{"ultra", "ULTRA", levelMax, levelHigh} {
		native, _ := Adapt("claude", nil, Policy([]config.Round{{Level: level}}, 1), worktree, "/review.json", nil)
		if strings.Contains(strings.ToLower(native.Command), "ultra") {
			t.Fatalf("level %q reached a multi-agent review: %q", level, native.Command)
		}
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
	native, delegated = Adapt("claude", overrides, Policy([]config.Round{{Level: levelLow}}, 1), worktree, "/review.json", nil)
	if !delegated || !strings.HasPrefix(native.Command, "/deep-review ") {
		t.Fatalf("a known kind whose CLI has moved should follow the override: %#v", native)
	}
	if native.Preamble != "" {
		t.Fatalf("an override is free text, so its instructions ride on the line: %q", native.Preamble)
	}
	// an empty override turns the native path off and returns that kind to the built-in prompt
	if _, delegated = Adapt("codex", overrides, Policy(nil, 1), worktree, "/review.json", nil); delegated {
		t.Fatal("an empty override should return the kind to the built-in prompt")
	}
}

func TestARoundMayNameADifferentCommand(t *testing.T) {
	rounds := []config.Round{{Level: levelHigh, Command: "security-review"}}
	native, _ := Adapt("claude", nil, Policy(rounds, 1), worktree, "/review.json", nil)
	if native.Command != "/security-review high" {
		t.Fatalf("command %q", native.Command)
	}
}
