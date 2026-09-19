package loop

import (
	"strings"
	"testing"
)

func TestParseScopeReadsEverySpecAndRejectsTheRest(t *testing.T) {
	for spec, want := range map[string]Scope{
		"":                   {Spec: "auto", Kind: "auto"},
		"auto":               {Spec: "auto", Kind: "auto"},
		"worktree":           {Spec: "worktree", Kind: "worktree"},
		"branch":             {Spec: "branch", Kind: "branch"},
		"branch:":            {Spec: "branch", Kind: "branch"},
		"branch:origin/main": {Spec: "branch:origin/main", Kind: "branch", BaseRef: "origin/main"},
		"text:docs/plan.md":  {Spec: "text:docs/plan.md", Kind: "text", Document: "docs/plan.md"},
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

// auto is what the panel shows before anyone has configured anything, so it has to pick the scope
// a person would have picked: the branch when there is one, and the working tree otherwise.
func TestAutoReviewsTheBranchWhenThereIsOneAndTheWorktreeWhenThereIsNot(t *testing.T) {
	repository := newRepository(t)
	auto, err := ParseScope("")
	if err != nil {
		t.Fatal(err)
	}

	// on the default branch with nothing else to compare against, auto is the working tree
	onMain, err := auto.Resolve(t.Context(), nil, repository)
	if err != nil || onMain.Branch() || onMain.start() != "HEAD" {
		t.Fatalf("on main: %#v %v", onMain, err)
	}
	if got := onMain.Describe(); got != "auto → uncommitted changes" {
		t.Fatalf("described %q", got)
	}

	// a branch with a commit of its own is reviewed as a branch, uncommitted changes included
	base := strings.TrimSpace(run(t, repository, "rev-parse", "HEAD"))
	run(t, repository, "checkout", "-b", "feature")
	writeRepoFile(t, repository, "tracked.txt", "two\n")
	run(t, repository, "commit", "-am", "on the branch")
	onBranch, err := auto.Resolve(t.Context(), nil, repository)
	if err != nil || !onBranch.Branch() || onBranch.Base != base || onBranch.BaseRef != "main" {
		t.Fatalf("on a branch: %#v %v", onBranch, err)
	}
	if got := onBranch.Describe(); !strings.HasPrefix(got, "auto → branch vs main at ") {
		t.Fatalf("described %q", got)
	}

	// a branch with no commits of its own has nothing a worktree diff would miss
	run(t, repository, "checkout", "main")
	run(t, repository, "checkout", "-b", "empty")
	fresh, err := auto.Resolve(t.Context(), nil, repository)
	if err != nil || fresh.Branch() {
		t.Fatalf("on a branch with no commits: %#v %v", fresh, err)
	}
}

func TestAutoFallsBackToTheWorktreeOutsideGit(t *testing.T) {
	auto, err := ParseScope("auto")
	if err != nil {
		t.Fatal(err)
	}
	// resolving must never fail on a directory git knows nothing about: the run is what reports
	// that, and it reports it about the review, not about the scope
	resolved, err := auto.Resolve(t.Context(), nil, t.TempDir())
	if err != nil || resolved.Branch() || resolved.Text() {
		t.Fatalf("%#v %v", resolved, err)
	}
}
