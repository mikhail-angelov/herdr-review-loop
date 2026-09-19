package loop

import (
	"strings"
	"testing"
)

func TestParseScopeReadsEverySpecAndRejectsTheRest(t *testing.T) {
	for spec, want := range map[string]Scope{
		"":                   {Spec: "worktree"},
		"worktree":           {Spec: "worktree"},
		"branch":             {Spec: "branch"},
		"branch:":            {Spec: "branch"},
		"branch:origin/main": {Spec: "branch:origin/main", BaseRef: "origin/main"},
		"text:docs/plan.md":  {Spec: "text:docs/plan.md", Document: "docs/plan.md"},
	} {
		got, err := ParseScope(spec)
		if err != nil || got != want {
			t.Errorf("ParseScope(%q) = %#v, %v; want %#v", spec, got, err, want)
		}
	}
	for _, spec := range []string{"commits", "branch:--upload-pack=x", "text:", "text:../outside"} {
		if _, err := ParseScope(spec); err == nil {
			t.Errorf("ParseScope(%q) was accepted", spec)
		}
	}
}

func TestBranchScopeIsJudgedAgainstWhereTheBranchLeftItsBase(t *testing.T) {
	repository := newRepository(t)
	base := strings.TrimSpace(run(t, repository, "rev-parse", "HEAD"))
	run(t, repository, "checkout", "-b", "feature")
	writeRepoFile(t, repository, "tracked.txt", "two\n")
	run(t, repository, "commit", "-am", "on the branch")
	// the base branch moving after the branch left it must not move what the run reviews
	run(t, repository, "checkout", "main")
	writeRepoFile(t, repository, "other.txt", "elsewhere\n")
	run(t, repository, "add", "other.txt")
	run(t, repository, "commit", "-m", "on main")
	run(t, repository, "checkout", "feature")

	scope, err := ParseScope("branch")
	if err != nil {
		t.Fatal(err)
	}
	scope, err = scope.Resolve(t.Context(), nil, repository)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Base != base || scope.BaseRef != "main" {
		t.Fatalf("resolved %q at %q, want main at %q", scope.BaseRef, scope.Base, base)
	}
	if !strings.Contains(scope.Command(), "git diff "+base) {
		t.Fatalf("command %q does not compare against the merge base", scope.Command())
	}
	if !strings.Contains(scope.Review(), base) || !strings.Contains(scope.Review(), "main") {
		t.Fatalf("the reviewer is not told what to compare: %q", scope.Review())
	}
}

func TestBranchScopeRefusesABaseItCannotTellFromTheBranchItself(t *testing.T) {
	repository := newRepository(t)
	scope, err := ParseScope("branch")
	if err != nil {
		t.Fatal(err)
	}
	// still on main, with no remote: every candidate is HEAD, and reviewing the uncommitted
	// changes under a name that promised the whole branch would be a lie
	if _, resolveErr := scope.Resolve(t.Context(), nil, repository); resolveErr == nil {
		t.Fatal("a branch scope resolved against itself")
	}
	named, err := ParseScope("branch:nosuchref")
	if err != nil {
		t.Fatal(err)
	}
	if _, resolveErr := named.Resolve(t.Context(), nil, repository); resolveErr == nil {
		t.Fatal("a base that does not exist was accepted")
	}
}

func TestPanelScopeNamesWhatWouldBeReviewedAndHowMuchOfIt(t *testing.T) {
	repository := newRepository(t)
	base := strings.TrimSpace(run(t, repository, "rev-parse", "HEAD"))
	run(t, repository, "checkout", "-b", "feature")
	writeRepoFile(t, repository, "tracked.txt", "two\nthree\n")
	run(t, repository, "commit", "-am", "on the branch")
	writeRepoFile(t, repository, "untracked.txt", "new\n")

	worktreeScope, worktreeChanges := PanelScope(t.Context(), repository, "worktree")
	if worktreeScope != "uncommitted changes" || !strings.Contains(worktreeChanges, "1 new") {
		t.Fatalf("worktree: %q / %q", worktreeScope, worktreeChanges)
	}
	if strings.Contains(worktreeChanges, "files") {
		t.Fatalf("the committed change leaked into the worktree summary: %q", worktreeChanges)
	}

	branchScope, branchChanges := PanelScope(t.Context(), repository, "branch")
	if !strings.Contains(branchScope, "branch vs main") || !strings.Contains(branchScope, base[:12]) {
		t.Fatalf("branch: %q", branchScope)
	}
	if !strings.Contains(branchChanges, "1 files") || !strings.Contains(branchChanges, "+2") || !strings.Contains(branchChanges, "1 new") {
		t.Fatalf("branch changes: %q", branchChanges)
	}

	broken, changes := PanelScope(t.Context(), repository, "staged")
	if !strings.HasPrefix(broken, "staged — ") || changes != "" {
		t.Fatalf("a scope nobody can resolve must say so: %q / %q", broken, changes)
	}
}
