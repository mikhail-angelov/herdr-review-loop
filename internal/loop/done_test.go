package loop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// This file encodes SPEC-v2 §13's definition of done as tests, rather than leaving those criteria
// as prose nobody re-checks. The individual features have their own tests; these assert the
// properties the specification says must hold whatever path a run takes.

// closedActions is §5.4's action set. Every finding in a report carries exactly one of these.
var closedActions = []string{
	ActionApplied, ActionRejected, ActionDeferred, ActionMissing,
	ActionFiltered, ActionPreExisting, ActionUnreviewed,
}

// raisedFindings counts what a reviewer's output actually put into a run: the findings it asked
// for and the pre-existing ones it recorded, whether or not any author ever saw them.
func raisedFindings(t *testing.T, reviews []string) int {
	t.Helper()
	total := 0
	for _, raw := range reviews {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var review Review
		if json.Unmarshal([]byte(raw), &review) != nil {
			continue // unparsable output raised nothing the loop could hold
		}
		normalized, _ := review.normalize()
		total += len(normalized.Findings) + len(normalized.PreExisting)
	}
	return total
}

// TestNothingARunProducedIsLost is §13.6, the one criterion stated as an invariant: every finding
// the reviewer ever raised is in report.json with the action taken on it, including the ones no
// author ever saw. It is checked across every way a run can end, because a report that accounted
// only for the findings someone answered would be silent about exactly the rounds that went wrong.
func TestNothingARunProducedIsLost(t *testing.T) {
	finding := func(file, title, verdict string) string {
		return `{"file":"` + file + `","line":1,"category":"correctness","severity":"high","verdict":"` + verdict + `","title":"` + title + `"}`
	}
	for _, test := range []struct {
		name       string
		reviews    []string
		decisions  []string
		rounds     int
		minVerdict string
		wantCode   int
	}{
		{
			name:     "clean on the first round",
			reviews:  []string{`{"status":"clean","findings":[]}`},
			rounds:   1,
			wantCode: ExitClean,
		},
		{
			name:      "clean after one author phase",
			reviews:   []string{`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `]}`, `{"status":"clean","findings":[]}`},
			decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`},
			rounds:    2,
			wantCode:  ExitClean,
		},
		{
			name:     "the round budget is spent",
			reviews:  []string{`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `]}`},
			rounds:   1,
			wantCode: ExitFindings,
		},
		{
			name: "the reviewer asks a question",
			reviews: []string{`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `],` +
				`"open_questions":[{"question":"is the 5m budget meant to cover a reformat turn?"}]}`},
			rounds:   3,
			wantCode: ExitBlocked,
		},
		{
			name:       "everything is below the verdict bar",
			reviews:    []string{`{"status":"findings","findings":[` + finding("a.go", "might be stale", "plausible") + `]}`},
			rounds:     1,
			minVerdict: config.VerdictConfirmed,
			wantCode:   ExitClean,
		},
		{
			name: "some findings are filtered and the rest are decided",
			reviews: []string{`{"status":"findings","findings":[` +
				finding("a.go", "leaks a handle", "confirmed") + `,` + finding("b.go", "might be stale", "plausible") + `]}`,
				`{"status":"clean","findings":[]}`},
			decisions:  []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`},
			rounds:     2,
			minVerdict: config.VerdictConfirmed,
			wantCode:   ExitClean,
		},
		{
			name: "the reviewer reports pre-existing findings",
			reviews: []string{`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `],` +
				`"pre_existing":[` + finding("old.go", "predates the diff", "confirmed") + `]}`},
			rounds:   1,
			wantCode: ExitFindings,
		},
		{
			name:      "the author decides nothing about one id",
			reviews:   []string{`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `,` + finding("b.go", "stale comment", "confirmed") + `]}`, `{"status":"clean","findings":[]}`},
			decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`},
			rounds:    2,
			wantCode:  ExitClean,
		},
		{
			name: "the pair gets stuck over one finding",
			reviews: []string{
				`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `]}`,
				`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `]}`,
				`{"status":"findings","findings":[` + finding("a.go", "leaks a handle", "confirmed") + `]}`,
			},
			decisions: []string{
				`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`,
				`{"decisions":[{"id":"r02-1","action":"applied","note":"closed it again"}]}`,
				`{"decisions":[{"id":"r03-1","action":"applied","note":"and again"}]}`,
			},
			rounds:   10,
			wantCode: ExitFindings,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &runClient{agents: pairedAgents(), reviews: slices.Clone(test.reviews), decisions: slices.Clone(test.decisions)}
			values := quickValues()
			values.MaxIterations = test.rounds
			if test.minVerdict != "" {
				values.MinVerdict = test.minVerdict
			}
			run, _, state := newRun(t, client, values)
			err := run.Execute(context.Background(), false)
			if ExitCode(err) != test.wantCode {
				t.Fatalf("got %v (code %d), want code %d", err, ExitCode(err), test.wantCode)
			}
			report := latestReport(t, state)

			// every raised finding is accounted for, exactly once
			consumed := len(test.reviews) - len(client.reviews)
			if want := raisedFindings(t, test.reviews[:consumed]); len(report.Findings) != want {
				t.Fatalf("the report holds %d finding(s), want the %d the reviewer raised: %+v", len(report.Findings), want, report.Findings)
			}
			seen := map[string]bool{}
			for _, entry := range report.Findings {
				if entry.ID == "" || entry.Fingerprint == "" {
					t.Fatalf("a finding reached the report without an id or fingerprint: %+v", entry)
				}
				if seen[entry.ID] {
					t.Fatalf("%s is in the report twice", entry.ID)
				}
				seen[entry.ID] = true
				if !slices.Contains(closedActions, entry.Action) {
					t.Fatalf("%s carries %q, which is outside the closed action set", entry.ID, entry.Action)
				}
				if entry.Round < 1 {
					t.Fatalf("%s does not say which round raised it: %+v", entry.ID, entry)
				}
			}
			if report.ExitCode != test.wantCode || report.Outcome == "" {
				t.Fatalf("the report does not say how the run ended: %+v", report)
			}
		})
	}
}

// TestAFinishedRunLeavesOnlyCodeInTheWorkingTree is §13.2. The run directory is the only thing the
// loop writes inside the repository, and finish removes it.
func TestAFinishedRunLeavesOnlyCodeInTheWorkingTree(t *testing.T) {
	client := &runClient{
		agents:    pairedAgents(),
		reviews:   []string{`{"status":"findings","findings":[{"file":"a.go","line":1,"category":"correctness","title":"leaks"}]}`, `{"status":"clean","findings":[]}`},
		decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	run, repository, _ := newRun(t, client, values)
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// during a run the only thing under the repository is the run directory
	entries, err := os.ReadDir(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != PluginDir {
			t.Fatalf("the loop left %s in the working tree", entry.Name())
		}
	}
	for _, gone := range []string{"review.md", "review-summary.md", "scope.md"} {
		if _, statErr := os.Lstat(filepath.Join(repository, gone)); !os.IsNotExist(statErr) {
			t.Fatalf("%s is in the working tree: %v", gone, statErr)
		}
	}
	// and finish takes even that away
	dir, err := OpenRunDir(repository)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repository, RunSubdir)); !os.IsNotExist(err) {
		t.Fatalf("the run directory survived finish: %v", err)
	}
}

// TestTheLoopCarriesNoReviewTaxonomyOfItsOwn is §13.7. The built-in prompt explains the contract's
// fields and how hard to look; it must not say what kinds of problem to look for, because that
// belongs to the reviewer and to the project's own CLAUDE.md or AGENTS.md.
func TestTheLoopCarriesNoReviewTaxonomyOfItsOwn(t *testing.T) {
	// a taxonomy is a list of problem kinds; these are the words the v1 prompt used
	taxonomy := []string{
		"race", "deadlock", "unhandled error", "security", "injection", "off-by-one",
		"missing test", "memory leak", "null", "input validation", "performance",
	}
	prompts := map[string]string{
		"review": ReviewPrompt(worktree, defaultPolicy(t, 1), 3, "/review.json", nil),
		"fix":    FixPrompt(worktree, defaultPolicy(t, 1), "/review.json", "/decisions.json", []string{"r01-1"}),
		"schema": reviewSchema + "\n" + reviewRules,
	}
	native, _ := Adapt("claude", nil, defaultPolicy(t, 1), worktree, "/review.json", nil)
	prompts["native preamble"] = native.Preamble
	prompts["capture"] = native.Capture
	for name, prompt := range prompts {
		lowered := strings.ToLower(prompt)
		for _, word := range taxonomy {
			if strings.Contains(lowered, word) {
				t.Errorf("the %s prompt names %q, which is the reviewer's own taxonomy to decide:\n%s", name, word, prompt)
			}
		}
	}
	// the text: scope is the one exception the specification allows, because no native command
	// covers document review
	document, err := ParseScope("text:docs/plan.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.Review(), "contradictions") {
		t.Errorf("the text: scope should still say what document review means: %s", document.Review())
	}
}

// TestEveryEndingIsDistinguishableFromTheExitCodeAlone is §13.9.
func TestEveryEndingIsDistinguishableFromTheExitCodeAlone(t *testing.T) {
	codes := []int{ExitClean, ExitFindings, ExitTool, ExitBlocked, ExitCanceled, ExitAgent}
	seen := map[int]bool{}
	for _, code := range codes {
		if seen[code] {
			t.Fatalf("two endings share exit code %d", code)
		}
		seen[code] = true
	}
	if ExitCode(nil) != ExitClean {
		t.Fatal("a run that returned no error is not clean")
	}
	// an error that names no code is a tool error: everything the loop decides is wrapped where
	// it is decided, so an unwrapped one came from the setup
	if ExitCode(os.ErrNotExist) != ExitTool {
		t.Fatal("an unwrapped failure is not reported as a tool error")
	}
}
