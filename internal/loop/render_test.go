package loop

import (
	"strings"
	"testing"
)

func archivedDocument(t *testing.T) RoundDocument {
	t.Helper()
	state := t.TempDir()
	review := Review{
		Status: StatusFindings,
		Findings: []Finding{
			{File: "internal/loop/run.go", Line: 88, Category: "correctness", Severity: SeverityHigh, Verdict: VerdictConfirmed, Title: "context is not canceled on the timeout path", Body: "why it matters", Fix: "cancel it in the defer"},
			{File: "README.md", Category: "docs", Severity: SeverityLow, Verdict: VerdictPlausible, Title: "stale link", Regression: true},
		},
		OpenQuestions: []OpenQuestion{{Question: "is the stall budget meant to cover a reformat turn?", File: "internal/loop/run.go", Line: 12}},
		PreExisting:   []Finding{{File: "old.go", Severity: SeverityMedium, Title: "was already true"}},
	}
	review.Identify(1)
	decisions := Decisions{
		Tests:     Tests{Ran: true, Outcome: "go test ./... passed"},
		Decisions: []Decision{{ID: "r01-1", Action: ActionApplied, Note: "fixed, test added"}, {ID: "r01-2", Action: ActionMissing}},
	}
	archiveRound(t, state, "run", 1, review, decisions)
	document, found := LoadRound(state, "run", 1)
	if !found {
		t.Fatal("the archived round could not be loaded")
	}
	return document
}

func TestRenderMarkdownShowsEveryFindingWithWhatBecameOfIt(t *testing.T) {
	text := RenderMarkdown(archivedDocument(t))
	for _, want := range []string{
		"# run run · round 1",
		"## r01-1 · high · internal/loop/run.go:88",
		"context is not canceled on the timeout path",
		"_confirmed · correctness_",
		"fix: cancel it in the defer",
		"author: **applied** — fixed, test added",
		"## r01-2 · low · README.md",
		"regression",
		"author: **missing**",
		"## open questions",
		"is the stall budget meant to cover a reformat turn?",
		"internal/loop/run.go:12",
		"## pre-existing",
		"was already true",
		"## tests",
		"go test ./... passed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered round is missing %q:\n%s", want, text)
		}
	}
}

func TestRenderMarkdownNamesACleanRoundAsClean(t *testing.T) {
	text := RenderMarkdown(RoundDocument{Run: "run", Round: 3, Review: Review{Status: StatusClean}})
	if !strings.Contains(text, "**clean**") {
		t.Fatalf("rendered round %q", text)
	}
}

func TestRenderJSONCarriesBothHalvesOfTheRound(t *testing.T) {
	text, err := RenderJSON(archivedDocument(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"review"`, `"decisions"`, `"fingerprint"`, `"r01-1"`} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered JSON is missing %q:\n%s", want, text)
		}
	}
}

func TestLoadRoundReportsAMissingRound(t *testing.T) {
	if _, found := LoadRound(t.TempDir(), "run", 1); found {
		t.Fatal("a round that was never archived was loaded")
	}
}

func TestRenderDecoratesThroughTheStyleItIsGiven(t *testing.T) {
	document := archivedDocument(t)
	styled := Render(document, Style{
		Head:     func(text string) string { return "<h>" + text + "</h>" },
		Severity: func(severity string) string { return "<s>" + severity + "</s>" },
		Action:   func(action string) string { return "<a>" + action + "</a>" },
	})
	for _, want := range []string{"<h>r01-1</h>", "<s>high</s>", "<a>applied</a>", "<h>tests</h>"} {
		if !strings.Contains(styled, want) {
			t.Errorf("the style was not applied: %q missing from\n%s", want, styled)
		}
	}
	// a style that leaves a field unset renders that part plain rather than panicking, so a pane
	// can decorate only what it has a color for
	if strings.Contains(styled, "**") || strings.Contains(styled, "# run") {
		t.Errorf("markdown markers survived a style that replaced them:\n%s", styled)
	}
	if !strings.Contains(styled, "context is not canceled on the timeout path") {
		t.Errorf("the unstyled title was dropped:\n%s", styled)
	}
}
