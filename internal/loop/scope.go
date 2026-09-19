package loop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// Scope is what a run reviews. Every scope ends at the working tree, because the author may only
// edit the working tree: a comparison that stopped at the index or at HEAD could not see the
// author's fixes, so round two would re-review a byte-identical diff and the loop could never
// converge. What differs between scopes is where the comparison starts.
//
// worktree starts at HEAD: the uncommitted changes. branch starts at the commit this branch and
// its base last had in common, so the whole of a branch's work is reviewed at once, committed and
// uncommitted alike — and the author's fixes are included whether it commits them or not.
//
// text: earns its place by being genuinely different — the reviewed thing is a whole document
// rather than a diff, and the author edits that document.
type Scope struct {
	Spec string
	// Document is the repository-relative path under review, empty for the worktree scope.
	Document string
	// Absolute is the path the agents are given, which they can write from any working directory.
	Absolute string
	// BaseRef is the branch a branch scope is judged against, and Base the commit it and HEAD last
	// had in common. Base is resolved once, before the first round: a base branch that moves
	// mid-run must not move what the round after it reviews.
	BaseRef string
	Base    string
}

// ParseScope reads a scope specification. It does not touch the filesystem; Resolve does that,
// once the repository is open.
func ParseScope(spec string) (Scope, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == config.ScopeWorktree {
		return Scope{Spec: config.ScopeWorktree}, nil
	}
	if spec == config.ScopeBranch || spec == config.ScopeBranchPrefix {
		return Scope{Spec: config.ScopeBranch}, nil
	}
	if ref, isBranch := strings.CutPrefix(spec, config.ScopeBranchPrefix); isBranch {
		ref = strings.TrimSpace(ref)
		// a ref is passed to git as an argument, so a leading dash would be read as a flag
		if strings.HasPrefix(ref, "-") {
			return Scope{}, fmt.Errorf("scope base %q is not a ref", ref)
		}
		return Scope{Spec: config.ScopeBranchPrefix + ref, BaseRef: ref}, nil
	}
	path, isText := strings.CutPrefix(spec, config.ScopeTextPrefix)
	if !isText {
		return Scope{}, fmt.Errorf("unknown scope %q: expected %s, %s[:<ref>] or %s<path>", spec, config.ScopeWorktree, config.ScopeBranch, config.ScopeTextPrefix)
	}
	path = strings.TrimSpace(strings.TrimPrefix(filepath.ToSlash(path), "./"))
	if path == "" {
		return Scope{}, fmt.Errorf("scope %s needs a path", config.ScopeTextPrefix)
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if filepath.IsAbs(path) || clean == ".." || strings.HasPrefix(clean, "../") {
		return Scope{}, fmt.Errorf("scope document %q must be inside the repository", path)
	}
	return Scope{Spec: config.ScopeTextPrefix + clean, Document: clean}, nil
}

// Text reports whether this run reviews a document rather than a diff.
func (s Scope) Text() bool { return s.Document != "" }

// Branch reports whether this run reviews a branch rather than the uncommitted changes alone.
func (s Scope) Branch() bool {
	return s.Spec == config.ScopeBranch || strings.HasPrefix(s.Spec, config.ScopeBranchPrefix)
}

// Resolve checks that the scope names something reviewable and fills in the absolute path the
// agents are given. A document that is not there is the user's setup, so it fails before the lock.
func (s Scope) Resolve(ctx context.Context, dir *RunDir, repository string) (Scope, error) {
	if s.Branch() {
		base, ref, err := resolveBase(ctx, repository, s.BaseRef)
		if err != nil {
			return Scope{}, err
		}
		s.Base, s.BaseRef = base, ref
		return s, nil
	}
	if !s.Text() {
		return s, nil
	}
	absolute, err := dir.Document(s.Document)
	if err != nil {
		return Scope{}, err
	}
	s.Absolute = absolute
	return s, nil
}

// baseCandidates are the refs a branch scope tries when none was named, in the order a branch is
// most likely to have come from one. The remote's own default branch is asked for first, because
// it is the only one of these the repository actually states rather than the loop guessing.
var baseCandidates = []string{"origin/main", "origin/master", "main", "master"}

// resolveBase finds the commit this branch left its base at. A candidate that is HEAD itself is
// skipped rather than used: it would mean the base is this branch, and the run would silently
// review the uncommitted changes under a name that promised the whole branch.
func resolveBase(ctx context.Context, repository, ref string) (base, named string, err error) {
	head, err := git(ctx, repository, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("scope %s needs a git repository with a commit: %w", config.ScopeBranch, err)
	}
	if ref != "" {
		base, err = mergeBase(ctx, repository, ref, head)
		return base, ref, err
	}
	for _, candidate := range append(remoteDefaultBranch(ctx, repository), baseCandidates...) {
		base, err = mergeBase(ctx, repository, candidate, head)
		if err != nil || base == head {
			continue
		}
		return base, candidate, nil
	}
	return "", "", errors.New("could not tell which branch this one came from: name it, as `--scope branch:<ref>`")
}

// remoteDefaultBranch is what origin says its default branch is, when it says anything.
func remoteDefaultBranch(ctx context.Context, repository string) []string {
	target, err := git(ctx, repository, nil, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
	if err != nil || target == "" {
		return nil
	}
	return []string{target}
}

func mergeBase(ctx context.Context, repository, ref, head string) (string, error) {
	if _, err := git(ctx, repository, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		return "", fmt.Errorf("scope base %q is not a commit this repository has", ref)
	}
	base, err := git(ctx, repository, nil, "merge-base", ref, head)
	if err != nil {
		return "", fmt.Errorf("%s and HEAD have no common commit: %w", ref, err)
	}
	return base, nil
}

// Command is what produces the reviewed material, recorded in the manifest so an archived run says
// what it was looking at without the tree it ran against.
func (s Scope) Command() string {
	if s.Text() {
		return "read " + s.Document
	}
	return "git status --porcelain && git diff " + s.start()
}

// start is where the comparison begins: HEAD for the uncommitted changes, and the merge base for a
// whole branch. It is a commit id rather than a ref for the branch scope, so every round of a run
// is judged against the same point even if the base branch moves while the run is going.
func (s Scope) start() string {
	if s.Branch() && s.Base != "" {
		return s.Base
	}
	return "HEAD"
}

// Review is the block that tells the reviewer what to read. For a diff the loop names the command
// rather than inlining the output, or round three carries a prompt large enough to undo what the
// session reset bought. The diff is taken against HEAD: a plain `git diff` hides everything the
// user staged, and a staged change the reviewer never saw could come back clean. For a document it
// says what document review means, which is the one place the loop still says what to look for: no
// native review command covers it.
func (s Scope) Review() string {
	if s.Branch() {
		return fmt.Sprintf("Review everything this branch changes relative to %s: read `git status --porcelain` and `git diff %s`, and read any untracked file they name in full. That commit is where this branch and %s last agreed, so the diff is the whole of the branch's work — what is committed on it and what is not yet committed alike.", s.BaseRef, s.start(), s.BaseRef)
	}
	if !s.Text() {
		return "Review the uncommitted changes in the working tree: read `git status --porcelain` and `git diff HEAD`, and read any untracked file they name in full."
	}
	return fmt.Sprintf("Review the document at %s as a whole, not as a diff. Read all of it. Report gaps, contradictions between its own sections, cases it leaves unhandled, and claims it does not support. Judge it against what it sets out to do, not against a style you would have chosen.", s.Absolute)
}

// Author is what the author is told it may change. Both scopes end in the working tree; only the
// part of it that is under review differs.
func (s Scope) Author() string {
	if !s.Text() {
		return "Apply your changes to the working tree."
	}
	return "Apply your changes to " + s.Absolute + " and to nothing else; that document is the whole of what is under review."
}

// shortstat reads `git diff --shortstat`, which is the cheapest question git answers about how
// much a scope covers.
var shortstat = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

// PanelScope is what the panel says a run would review: the scope as configured, resolved far
// enough to name what it compares against, and how much has changed since that point. It answers
// the question a panel exists to raise — is this the change I meant to review — before a run is
// started rather than after it comes back having reviewed the wrong half.
//
// It is best-effort and never fails: a scope that cannot be resolved says so in the line it would
// have used, because a panel that hides a broken setting is worse than one that names it.
func PanelScope(ctx context.Context, repository, spec string) (scope, changes string) {
	parsed, err := ParseScope(spec)
	switch {
	case err != nil:
		return spec + " — " + err.Error(), ""
	case parsed.Text():
		return "document " + parsed.Document, ""
	case parsed.Branch():
		resolved, resolveErr := parsed.Resolve(ctx, nil, repository)
		if resolveErr != nil {
			return parsed.Spec + " — " + resolveErr.Error(), ""
		}
		return fmt.Sprintf("branch vs %s at %s", resolved.BaseRef, shortCommit(resolved.Base)), changeSummary(ctx, repository, resolved.start())
	default:
		return "uncommitted changes", changeSummary(ctx, repository, "HEAD")
	}
}

// changeSummary is how much the scope covers, in the shape a narrow panel can hold. Untracked
// files are counted separately because git's own diff does not see them at all, and a new file is
// the most common thing under review.
func changeSummary(ctx context.Context, repository, start string) string {
	parts := make([]string, 0, 3)
	stat, err := git(ctx, repository, nil, "diff", "--shortstat", start)
	if err != nil {
		return ""
	}
	if match := shortstat.FindStringSubmatch(stat); match != nil {
		parts = append(parts, match[1]+" files")
		if lines := changedLines(match[2], match[3]); lines != "" {
			parts = append(parts, lines)
		}
	}
	if untracked := untrackedCount(ctx, repository); untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d new", untracked))
	}
	if len(parts) == 0 {
		return "nothing changed yet"
	}
	return strings.Join(parts, " · ")
}

func changedLines(insertions, deletions string) string {
	parts := make([]string, 0, 2)
	if insertions != "" {
		parts = append(parts, "+"+insertions)
	}
	if deletions != "" {
		parts = append(parts, "−"+deletions)
	}
	return strings.Join(parts, " ")
}

// untrackedCount counts the files git is not tracking yet. The run directory is excluded through
// info/exclude, so a run's own files are never among them.
func untrackedCount(ctx context.Context, repository string) int {
	status, err := git(ctx, repository, nil, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "??") {
			count++
		}
	}
	return count
}

// shortCommit is a commit id at the length a person reads rather than the length git stores.
func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
