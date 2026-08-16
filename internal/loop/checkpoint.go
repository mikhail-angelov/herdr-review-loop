package loop

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// RefPrefix namespaces every checkpoint this plugin writes. Refs outside
// refs/heads and refs/tags do not appear in git branch or git log, so a run
// leaves no trace in the history the user actually reads.
const RefPrefix = "refs/herdr-review-loop/"

// CheckpointRetention is how many runs keep their checkpoints in a repository.
// Older refs are dropped at the start of a run, which lets git collect the
// objects they were holding alive.
const CheckpointRetention = 5

// Checkpoints snapshots the working tree without touching it. Every write goes
// through a temporary index named by GIT_INDEX_FILE, so the user's staged
// changes, the stash stack, HEAD and the worktree are all left exactly as they
// were. It also means no index.lock is taken: an agent running git in the same
// repository during a snapshot cannot collide with it.
//
// git stash create would be shorter and is wrong here — it does not capture
// untracked files, and agents create files constantly, so its snapshots would
// silently omit most of what a round produced.
type Checkpoints struct {
	Repository string
	RunID      string
	Note       func(string)
	// Enabled is set once, from Available, so a repository that is not a git
	// work tree costs one probe per run instead of a failure per round.
	Enabled bool
}

// RefName is the ref a run's round is stored under.
func RefName(runID string, round int) string {
	return fmt.Sprintf("%s%s/round-%02d", RefPrefix, runID, round)
}

// Available reports whether the repository is a git work tree, and so whether checkpoints can be
// taken at all.
func (c Checkpoints) Available(ctx context.Context) bool {
	out, err := c.git(ctx, nil, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// Tree records the current worktree as a git tree object and returns its id, without touching the
// user's index, stash or HEAD. `git add -A` honors info/exclude, so the run directory stays out of
// it and a tree is exactly the author's changes.
//
// A plain `git diff` would omit every file the author newly created, and a new test is the most
// common thing an author creates, so the fix that mattered most would be missing from the patch;
// staging into a throwaway index is what includes them.
func (c Checkpoints) Tree(ctx context.Context) (string, error) {
	tree, _, err := c.tree(ctx)
	return tree, err
}

// Diff is the patch between two trees, new files included.
func (c Checkpoints) Diff(ctx context.Context, from, to string) (string, error) {
	if from == "" || to == "" || from == to {
		return "", nil
	}
	return c.git(ctx, nil, "diff", "--no-color", "--find-renames", from, to)
}

// tree stages the worktree into a temporary index and returns the tree it wrote, along with the
// environment naming that index so a caller can keep using it.
func (c Checkpoints) tree(ctx context.Context) (tree string, env []string, err error) {
	index, err := os.CreateTemp("", "herdr-review-loop-index-*")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temporary git index: %w", err)
	}
	path := index.Name()
	// git wants to create the index itself; an existing empty file is not a
	// valid index, so the placeholder is removed and only its name is used.
	_ = index.Close()
	_ = os.Remove(path)
	defer func() { _ = os.Remove(path) }()
	env = append(os.Environ(),
		"GIT_INDEX_FILE="+filepath.Clean(path),
		"GIT_AUTHOR_NAME=herdr-review-loop", "GIT_AUTHOR_EMAIL=herdr-review-loop@localhost",
		"GIT_COMMITTER_NAME=herdr-review-loop", "GIT_COMMITTER_EMAIL=herdr-review-loop@localhost",
	)
	if _, addErr := c.git(ctx, env, "add", "-A"); addErr != nil {
		return "", nil, addErr
	}
	tree, err = c.git(ctx, env, "write-tree")
	if err != nil {
		return "", nil, err
	}
	return tree, env, nil
}

// Save records the current worktree as round's checkpoint and returns its
// commit id. Round 0 is the baseline taken before the first fix, without which
// there is nothing to compare the first round against.
func (c Checkpoints) Save(ctx context.Context, round int) (string, error) {
	tree, env, err := c.tree(ctx)
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-m", fmt.Sprintf("herdr-review-loop %s round %02d", c.RunID, round)}
	if parent, headErr := c.git(ctx, env, "rev-parse", "--verify", "HEAD"); headErr == nil && parent != "" {
		args = append(args, "-p", parent)
	}
	commit, err := c.git(ctx, env, args...)
	if err != nil {
		return "", err
	}
	if _, refErr := c.git(ctx, nil, "update-ref", RefName(c.RunID, round), commit); refErr != nil {
		return "", refErr
	}
	return commit, nil
}

// SaveQuietly records a checkpoint and reports failure through the log instead
// of the run: a snapshot that could not be taken is worth knowing about, but a
// review that has already happened is not worth abandoning over one.
func (c Checkpoints) SaveQuietly(ctx context.Context, round int) {
	if !c.Enabled {
		return
	}
	if _, err := c.Save(ctx, round); err != nil && c.Note != nil {
		c.Note(fmt.Sprintf("checkpoint %d failed: %s", round, err))
	}
}

// Prune drops the checkpoints of every run beyond the retention limit. Run ids
// are timestamps, so the newest are the last in lexicographic order.
func (c Checkpoints) Prune(ctx context.Context, keep int) error {
	refs, err := c.refs(ctx, RefPrefix)
	if err != nil {
		return err
	}
	runs := map[string][]string{}
	var ids []string
	for ref := range refs {
		id, ok := runOfRef(ref)
		if !ok {
			continue
		}
		if _, seen := runs[id]; !seen {
			ids = append(ids, id)
		}
		runs[id] = append(runs[id], ref)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for index, id := range ids {
		if index < keep {
			continue
		}
		for _, ref := range runs[id] {
			if _, err := c.git(ctx, nil, "update-ref", "-d", ref); err != nil && c.Note != nil {
				c.Note("checkpoint prune failed: " + err.Error())
			}
		}
	}
	return nil
}

// Rounds returns the checkpointed rounds of a run, by round number.
func (c Checkpoints) Rounds(ctx context.Context) map[int]string {
	refs, err := c.refs(ctx, RefPrefix+c.RunID+"/")
	if err != nil {
		return nil
	}
	rounds := map[int]string{}
	for ref, commit := range refs {
		round, ok := roundOfRef(ref)
		if !ok {
			continue
		}
		rounds[round] = commit
	}
	return rounds
}

func (c Checkpoints) refs(ctx context.Context, prefix string) (map[string]string, error) {
	out, err := c.git(ctx, nil, "for-each-ref", "--format=%(refname) %(objectname)", prefix)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, commit, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok {
			refs[name] = commit
		}
	}
	return refs, nil
}

func runOfRef(ref string) (string, bool) {
	rest, ok := strings.CutPrefix(ref, RefPrefix)
	if !ok {
		return "", false
	}
	id, _, ok := strings.Cut(rest, "/")
	return id, ok
}

func roundOfRef(ref string) (int, bool) {
	index := strings.LastIndex(ref, "/round-")
	if index < 0 {
		return 0, false
	}
	round, err := strconv.Atoi(ref[index+len("/round-"):])
	return round, err == nil
}

func (c Checkpoints) git(ctx context.Context, env []string, args ...string) (string, error) {
	//nolint:gosec // git and its subcommands are literals here; only the repository path varies
	command := exec.CommandContext(ctx, "git", append([]string{"-C", c.Repository}, args...)...)
	command.Env = env
	var out, failure bytes.Buffer
	command.Stdout, command.Stderr = &out, &failure
	if err := command.Run(); err != nil {
		message := strings.TrimSpace(failure.String())
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], message)
	}
	return strings.TrimSpace(out.String()), nil
}
