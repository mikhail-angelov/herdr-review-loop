package loop

import (
	"strings"
	"testing"
)

func TestReviewPromptStatesTheContractAndNothingAboutTheLoopsFiles(t *testing.T) {
	prompt := ReviewPrompt(1, 3, "/repo/.review-loop/run/round-01/review.json", nil)
	for _, want := range []string{
		"/repo/.review-loop/run/round-01/review.json",
		`"open_questions"`,
		`"pre_existing"`,
		"round 1 of 3",
		"broad first pass",
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
	if !strings.Contains(ReviewPrompt(3, 3, "path", nil), "Closure pass") {
		t.Error("the third round is not a closure pass")
	}
	if strings.Contains(ReviewPrompt(1, 3, "path", nil), "settled") {
		t.Error("round 1 carries a decision journal it cannot have")
	}
}

func TestReviewPromptCarriesTheDecisionJournal(t *testing.T) {
	prompt := ReviewPrompt(2, 3, "path", []Settled{{Round: 1, Action: ActionRejected, Title: "context leak", Note: "canceled in the defer above"}})
	for _, want := range []string{"already settled", "rejected in round 1", "context leak", "canceled in the defer above"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestFixPromptNamesEveryIdAndTightensAfterEachRound(t *testing.T) {
	prompt := FixPrompt(1, "/review.json", "/decisions.json", []string{"r01-1", "r01-2"})
	for _, want := range []string{"/review.json", "/decisions.json", "r01-1, r01-2", "Decide every id exactly once", `"tests"`} {
		if !strings.Contains(prompt, want) {
			t.Errorf("fix prompt is missing %q", want)
		}
	}
	if !strings.Contains(prompt, "medium-severity") {
		t.Errorf("first-round policy is missing medium findings: %q", prompt)
	}
	if !strings.Contains(FixPrompt(3, "a", "b", nil), "only high-severity findings") {
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
