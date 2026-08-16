package loop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// Scope is what a run reviews. There are two, because the author may only edit the working tree,
// which makes the working tree the end of every comparison worth offering: a scope that stopped at
// the index or at HEAD could not see the author's fixes, so round two would re-review a
// byte-identical diff and the loop could never converge.
//
// text: earns its place by being genuinely different — the reviewed thing is a whole document
// rather than a diff, and the author edits that document.
type Scope struct {
	Spec string
	// Document is the repository-relative path under review, empty for the worktree scope.
	Document string
	// Absolute is the path the agents are given, which they can write from any working directory.
	Absolute string
}

// ParseScope reads a scope specification. It does not touch the filesystem; Resolve does that,
// once the repository is open.
func ParseScope(spec string) (Scope, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == config.ScopeWorktree {
		return Scope{Spec: config.ScopeWorktree}, nil
	}
	path, isText := strings.CutPrefix(spec, config.ScopeTextPrefix)
	if !isText {
		return Scope{}, fmt.Errorf("unknown scope %q: expected %s or %s<path>", spec, config.ScopeWorktree, config.ScopeTextPrefix)
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

// Resolve checks that the scope names something reviewable and fills in the absolute path the
// agents are given. A document that is not there is the user's setup, so it fails before the lock.
func (s Scope) Resolve(dir *RunDir) (Scope, error) {
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

// Command is what produces the reviewed material, recorded in the manifest so an archived run says
// what it was looking at without the tree it ran against.
func (s Scope) Command() string {
	if s.Text() {
		return "read " + s.Document
	}
	return "git status --porcelain && git diff HEAD"
}

// Review is the block that tells the reviewer what to read. For a diff the loop names the command
// rather than inlining the output, or round three carries a prompt large enough to undo what the
// session reset bought. The diff is taken against HEAD: a plain `git diff` hides everything the
// user staged, and a staged change the reviewer never saw could come back clean. For a document it
// says what document review means, which is the one place the loop still says what to look for: no
// native review command covers it.
func (s Scope) Review() string {
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
