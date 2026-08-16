package loop

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseReviewTakesTheLastObjectAndStripsFences(t *testing.T) {
	raw := "Here is the shape I will use:\n\n```json\n{\"status\":\"clean\",\"findings\":[]}\n```\n\nAnd here is the answer:\n\n```json\n" +
		`{"status":"findings","findings":[{"file":"./a/b.go","line":3,"title":"leaks a handle","severity":"HIGH"}]}` + "\n```\n"
	review, notes, err := ParseReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("notes %#v", notes)
	}
	if len(review.Findings) != 1 || review.Findings[0].File != "a/b.go" || review.Findings[0].Severity != SeverityHigh {
		t.Fatalf("review %#v", review)
	}
	if review.Findings[0].Verdict != VerdictConfirmed {
		t.Fatalf("verdict defaulted to %q", review.Findings[0].Verdict)
	}
}

func TestParseReviewFallsBackToMarkdown(t *testing.T) {
	raw := "STATUS: FINDINGS\n- [high] internal/loop/run.go:88 — context is not canceled — cancel it in the defer\n- [low] README.md — stale link — update it\n"
	review, notes, err := ParseReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(notes, "parse_fallback: markdown") {
		t.Fatalf("notes %#v", notes)
	}
	if len(review.Findings) != 2 {
		t.Fatalf("findings %#v", review.Findings)
	}
	if got := review.Findings[0]; got.Line != 88 || got.Fix != "cancel it in the defer" {
		t.Fatalf("first finding %#v", got)
	}
	if got := review.Findings[1]; got.Line != 0 || got.Severity != SeverityLow {
		t.Fatalf("second finding %#v", got)
	}
}

func TestParseReviewRejectsUnreadableOutput(t *testing.T) {
	if _, _, err := ParseReview("I could not complete the review, sorry."); !errors.Is(err, ErrNoReview) {
		t.Fatalf("got %v, want ErrNoReview", err)
	}
}

func TestParseReviewDropsWhatItCannotUse(t *testing.T) {
	raw := `{"status":"findings",
	  "findings":[
	    {"file":"../outside.go","title":"escapes"},
	    {"file":"/etc/passwd","title":"absolute"},
	    {"file":"ok.go","title":"kept"}
	  ],
	  "open_questions":[{"question":"   "},{"question":"is the budget meant to cover a reformat?"}]}`
	review, notes, err := ParseReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(review.Findings) != 1 || review.Findings[0].Title != "kept" {
		t.Fatalf("findings %#v", review.Findings)
	}
	if len(review.OpenQuestions) != 1 {
		t.Fatalf("questions %#v", review.OpenQuestions)
	}
	if got := strings.Join(notes, ","); !strings.Contains(got, "degraded: path") || !strings.Contains(got, "degraded: open-question") {
		t.Fatalf("notes %#v", notes)
	}
}

func TestResolveWeighsTheArraysAgainstTheStatus(t *testing.T) {
	for _, test := range []struct {
		name   string
		review Review
		want   Resolution
	}{
		{"clean means clean", Review{Status: StatusClean}, Clean},
		{"findings win over a clean status", Review{Status: StatusClean, Findings: []Finding{{Title: "x"}}}, Dirty},
		{"an empty findings array is never clean", Review{Status: StatusFindings}, Contradictory},
		{"an unknown status is never clean", Review{Status: "done"}, Contradictory},
		{"a question wins over everything", Review{Status: StatusClean, Findings: []Finding{{Title: "x"}}, OpenQuestions: []OpenQuestion{{Question: "?"}}}, Blocked},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.review.Resolve(); got != test.want {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestIdentifyIsStableAcrossRoundsForTheSameFinding(t *testing.T) {
	first := Review{Findings: []Finding{{File: "a.go", Category: "correctness", Title: "Context is NOT canceled, on the timeout path!"}}}
	second := Review{Findings: []Finding{{File: "z.go", Title: "other"}, {File: "a.go", Category: "correctness", Title: "context   is not canceled on the timeout path"}}}
	first.Identify(1)
	second.Identify(2)
	if first.Findings[0].ID != "r01-1" || second.Findings[1].ID != "r02-2" {
		t.Fatalf("ids %q %q", first.Findings[0].ID, second.Findings[1].ID)
	}
	if first.Findings[0].Fingerprint != second.Findings[1].Fingerprint {
		t.Fatalf("fingerprints differ: %q %q", first.Findings[0].Fingerprint, second.Findings[1].Fingerprint)
	}
	if first.Findings[0].Fingerprint == second.Findings[0].Fingerprint {
		t.Fatal("different findings share a fingerprint")
	}
	if len(first.Findings[0].Fingerprint) != 12 {
		t.Fatalf("fingerprint %q is not 12 characters", first.Findings[0].Fingerprint)
	}
}

func TestAuthorViewWithholdsWhatTheAuthorHasNoSayOver(t *testing.T) {
	review := Review{
		Status:      StatusFindings,
		Findings:    []Finding{{ID: "r01-1", Title: "act on me"}},
		PreExisting: []Finding{{ID: "r01-p1", Title: "already true"}},
	}
	view := review.AuthorView()
	if len(view.PreExisting) != 0 {
		t.Fatalf("pre-existing findings reached the author: %#v", view.PreExisting)
	}
	if len(view.Findings) != 1 || view.Findings[0].ID != "r01-1" {
		t.Fatalf("author view %#v", view)
	}
}
