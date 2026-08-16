package loop

import (
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// worktree is the default scope, which every prompt test but the text: one runs under.
var worktree = Scope{Spec: config.ScopeWorktree}

// defaultPolicy is the built-in narrowing, resolved for one round, which is what every prompt test
// wants: the prompts are what a real run's policy produces, not a policy invented for the test.
func defaultPolicy(t *testing.T, round int) RoundPolicy {
	t.Helper()
	return Policy(config.Resolve(config.Sources{}).Values.Rounds, round)
}

func TestReviewPromptStatesTheContractAndNothingAboutTheLoopsFiles(t *testing.T) {
	prompt := ReviewPrompt(worktree, defaultPolicy(t, 1), 3, "/repo/.review-loop/run/round-01/review.json", nil)
	for _, want := range []string{
		"/repo/.review-loop/run/round-01/review.json",
		`"open_questions"`,
		`"pre_existing"`,
		"round 1 of 3",
		"broad pass",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("review prompt is missing %q", want)
		}
	}
	for _, unwanted := range []string{"review.md", "review-summary.md", "Do not report findings about"} {
		if strings.Contains(prompt, unwanted) {
			t.Errorf("review prompt still mentions %q", unwanted)
		}
	}
}

func TestReviewPromptNarrowsAsRoundsGoOn(t *testing.T) {
	if !strings.Contains(ReviewPrompt(worktree, defaultPolicy(t, 3), 3, "path", nil), "Closure pass") {
		t.Error("the third round is not a closure pass")
	}
	if strings.Contains(ReviewPrompt(worktree, defaultPolicy(t, 1), 3, "path", nil), "settled") {
		t.Error("round 1 carries a decision journal it cannot have")
	}
}

func TestReviewPromptCarriesTheDecisionJournal(t *testing.T) {
	prompt := ReviewPrompt(worktree, defaultPolicy(t, 2), 3, "path", []Settled{{Round: 1, Action: ActionRejected, Title: "context leak", Note: "canceled in the defer above"}})
	for _, want := range []string{"already settled", "rejected in round 1", "context leak", "canceled in the defer above"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestReviewPromptCarriesTheRoundsOwnInstructions(t *testing.T) {
	rounds := []config.Round{{Level: "medium", Instructions: "only the public API surface"}}
	prompt := ReviewPrompt(worktree, Policy(rounds, 1), 3, "path", nil)
	if !strings.Contains(prompt, "only the public API surface") {
		t.Errorf("the round's instructions did not reach the reviewer:\n%s", prompt)
	}
}

func TestARegressionsOnlyRoundNamesItsBaseline(t *testing.T) {
	policy := Policy([]config.Round{{Level: "low", RegressionsOnly: true}}, 3)
	if strings.Contains(policy.Review(), "regression") {
		t.Error("a regressions-only round with no baseline should not ask for one")
	}
	policy.Baseline = "/repo/.review-loop/run/baseline.patch"
	review := policy.Review()
	for _, want := range []string{"/repo/.review-loop/run/baseline.patch", `"regression": true`, "do not raise anything that predates them"} {
		if !strings.Contains(review, want) {
			t.Errorf("regressions-only block is missing %q:\n%s", want, review)
		}
	}
}

func TestTheLastRoundEntryRepeats(t *testing.T) {
	rounds := []config.Round{{Level: "high"}, {Level: "low", RegressionsOnly: true}}
	for _, round := range []int{2, 5, 40} {
		policy := Policy(rounds, round)
		if policy.Level != "low" || !policy.RegressionsOnly || policy.Number != round {
			t.Fatalf("round %d resolved to %#v", round, policy)
		}
	}
}

func TestFixPromptNamesEveryIdAndTightensAfterEachRound(t *testing.T) {
	prompt := FixPrompt(worktree, defaultPolicy(t, 1), "/review.json", "/decisions.json", []string{"r01-1", "r01-2"})
	for _, want := range []string{"/review.json", "/decisions.json", "r01-1, r01-2", "Decide every id exactly once", `"tests"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("fix prompt is missing %q", want)
		}
	}
	if !strings.Contains(prompt, "medium-severity") {
		t.Errorf("first-round policy is missing medium findings: %q", prompt)
	}
	if !strings.Contains(FixPrompt(worktree, defaultPolicy(t, 3), "a", "b", nil), "only high-severity findings") {
		t.Error("the later-round policy was not tightened")
	}
}

func TestReformatPromptsAskForOneFileAndNoWork(t *testing.T) {
	review := ReviewReformatPrompt("/review.json", "no review could be read from the output")
	if !strings.Contains(review, "Do not review anything again") || !strings.Contains(review, `"status"`) {
		t.Errorf("review reformat prompt %q", review)
	}
	fix := FixReformatPrompt("/decisions.json", "no id was decided", []string{"r01-1"})
	if !strings.Contains(fix, "Do not change any code in this turn") || !strings.Contains(fix, "r01-1") {
		t.Errorf("fix reformat prompt %q", fix)
	}
}

func TestTheTextScopeReviewsADocumentAndPointsTheAuthorAtIt(t *testing.T) {
	scope, err := ParseScope("text:docs/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	scope.Absolute = "/repo/docs/plan.md"
	review := ReviewPrompt(scope, defaultPolicy(t, 1), 3, "/review.json", nil)
	for _, want := range []string{"/repo/docs/plan.md", "as a whole, not as a diff", "contradictions"} {
		if !strings.Contains(review, want) {
			t.Errorf("the text: review prompt is missing %q:\n%s", want, review)
		}
	}
	if strings.Contains(review, "git diff") {
		t.Errorf("the text: review prompt still asks for a diff:\n%s", review)
	}
	fix := FixPrompt(scope, defaultPolicy(t, 1), "/review.json", "/decisions.json", []string{"r01-1"})
	if !strings.Contains(fix, "/repo/docs/plan.md") || !strings.Contains(fix, "and to nothing else") {
		t.Errorf("the text: fix prompt does not confine the author to the document:\n%s", fix)
	}
	if !strings.Contains(ReviewPrompt(worktree, defaultPolicy(t, 1), 3, "/review.json", nil), "git diff") {
		t.Error("the worktree scope stopped naming the command that produces its diff")
	}
}

func TestTheWorktreeScopeCoversStagedChanges(t *testing.T) {
	// a plain `git diff` hides everything the user staged, and a staged change nobody reviewed
	// would come back clean
	for _, text := range []string{worktree.Review(), worktree.Command()} {
		if !strings.Contains(text, "git diff HEAD") {
			t.Errorf("the worktree scope compares against the index, not HEAD:\n%s", text)
		}
	}
}

func TestParseScopeRejectsWhatItCannotReach(t *testing.T) {
	if scope, err := ParseScope(""); err != nil || scope.Text() {
		t.Fatalf("an empty scope should be the worktree: %#v %v", scope, err)
	}
	for _, spec := range []string{"branch", "text:", "text:/etc/passwd", "text:../outside.md", "commits:HEAD~3.."} {
		if _, err := ParseScope(spec); err == nil {
			t.Errorf("ParseScope(%q) was accepted", spec)
		}
	}
	scope, err := ParseScope("text:./docs/../docs/plan.md")
	if err != nil || scope.Document != "docs/plan.md" {
		t.Fatalf("ParseScope = %#v %v", scope, err)
	}
}
