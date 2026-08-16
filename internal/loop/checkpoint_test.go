package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func newRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		command := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	writeRepoFile(t, repository, "tracked.txt", "one\n")
	run(t, repository, "add", "tracked.txt")
	run(t, repository, "commit", "-m", "initial")
	return repository
}

func run(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestCheckpointCapturesUntrackedFilesAndLeavesTheTreeAlone(t *testing.T) {
	repository := newRepository(t)
	writeRepoFile(t, repository, "tracked.txt", "two\n")
	writeRepoFile(t, repository, "new.txt", "created by the author\n")
	// A staged change and a stash entry that the snapshot must not disturb.
	run(t, repository, "add", "tracked.txt")
	statusBefore := run(t, repository, "status", "--porcelain")
	stashBefore := run(t, repository, "stash", "list")

	checkpoints := Checkpoints{Repository: repository, RunID: "run-1", Enabled: true}
	if !checkpoints.Available(t.Context()) {
		t.Fatal("a git work tree was not recognized")
	}
	commit, err := checkpoints.Save(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}

	files := run(t, repository, "ls-tree", "--name-only", "-r", commit)
	if !strings.Contains(files, "new.txt") {
		t.Fatalf("untracked file missing from the checkpoint: %q", files)
	}
	if got := run(t, repository, "show", commit+":tracked.txt"); got != "two" {
		t.Fatalf("checkpoint recorded the wrong content: %q", got)
	}
	if got := run(t, repository, "status", "--porcelain"); got != statusBefore {
		t.Fatalf("snapshot changed the working tree or index:\n%q\n%q", statusBefore, got)
	}
	if got := run(t, repository, "stash", "list"); got != stashBefore {
		t.Fatalf("snapshot touched the stash stack: %q", got)
	}
	if got := run(t, repository, "rev-parse", "--verify", RefName("run-1", 1)); got != commit {
		t.Fatalf("checkpoint ref points at %s, want %s", got, commit)
	}
}

func TestCheckpointIgnoresIgnoredFiles(t *testing.T) {
	repository := newRepository(t)
	writeRepoFile(t, repository, ".gitignore", "secret.txt\n")
	writeRepoFile(t, repository, "secret.txt", "not for the snapshot\n")

	checkpoints := Checkpoints{Repository: repository, RunID: "run-1", Enabled: true}
	commit, err := checkpoints.Save(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if files := run(t, repository, "ls-tree", "--name-only", "-r", commit); strings.Contains(files, "secret.txt") {
		t.Fatalf("an ignored file was captured: %q", files)
	}
}

func TestCheckpointRoundsAndPruneKeepTheNewestRuns(t *testing.T) {
	repository := newRepository(t)
	for _, id := range []string{"run-1", "run-2", "run-3"} {
		checkpoints := Checkpoints{Repository: repository, RunID: id, Enabled: true}
		for round := range 2 {
			if _, err := checkpoints.Save(t.Context(), round); err != nil {
				t.Fatal(err)
			}
		}
	}
	newest := Checkpoints{Repository: repository, RunID: "run-3", Enabled: true}
	if rounds := newest.Rounds(t.Context()); len(rounds) != 2 || rounds[0] == "" || rounds[1] == "" {
		t.Fatalf("unexpected rounds: %+v", rounds)
	}
	if err := newest.Prune(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	refs := run(t, repository, "for-each-ref", "--format=%(refname)", RefPrefix)
	if strings.Contains(refs, "run-1") {
		t.Fatalf("the oldest run kept its checkpoints: %q", refs)
	}
	for _, id := range []string{"run-2", "run-3"} {
		if !strings.Contains(refs, id) {
			t.Fatalf("%s lost its checkpoints: %q", id, refs)
		}
	}
}

// The retention limit counts the run that is starting, so a repository never
// holds more than CheckpointRetention runs of checkpoints, however many run.
func TestRunKeepsCheckpointsForAtMostTheRetentionLimit(t *testing.T) {
	repository := newRepository(t)
	loop := Run{Log: Log{StateDir: t.TempDir()}}
	for index := range CheckpointRetention + 2 {
		writeRepoFile(t, repository, "tracked.txt", strconv.Itoa(index))
		loop.checkpoints(t.Context(), repository, fmt.Sprintf("run-%02d", index))
	}

	ids := map[string]bool{}
	for _, ref := range strings.Split(run(t, repository, "for-each-ref", "--format=%(refname)", RefPrefix), "\n") {
		id, ok := runOfRef(ref)
		if !ok {
			t.Fatalf("unrecognized checkpoint ref: %q", ref)
		}
		ids[id] = true
	}
	if len(ids) != CheckpointRetention {
		t.Fatalf("kept %d runs of checkpoints, want %d: %v", len(ids), CheckpointRetention, ids)
	}
	if !ids[fmt.Sprintf("run-%02d", CheckpointRetention+1)] {
		t.Fatalf("the newest run lost its checkpoints: %v", ids)
	}
}

func TestCheckpointWorksBeforeTheFirstCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := t.TempDir()
	run(t, repository, "init", "--initial-branch=main")
	writeRepoFile(t, repository, "new.txt", "first\n")

	checkpoints := Checkpoints{Repository: repository, RunID: "run-1", Enabled: true}
	if _, err := checkpoints.Save(t.Context(), 0); err != nil {
		t.Fatalf("checkpoint failed in a repository with no HEAD: %v", err)
	}
}

func TestCheckpointsAreSkippedOutsideAGitWorkTree(t *testing.T) {
	checkpoints := Checkpoints{Repository: t.TempDir(), RunID: "run-1"}
	if checkpoints.Available(context.Background()) {
		t.Fatal("a plain directory was reported as a git work tree")
	}
	checkpoints.SaveQuietly(context.Background(), 0)
}

func TestCheckpointDoesNotLeaveItsTemporaryIndexBehind(t *testing.T) {
	repository := newRepository(t)
	before, err := filepath.Glob(filepath.Join(os.TempDir(), "herdr-review-loop-index-*"))
	if err != nil {
		t.Fatal(err)
	}
	checkpoints := Checkpoints{Repository: repository, RunID: "run-1", Enabled: true}
	if _, err = checkpoints.Save(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	after, err := filepath.Glob(filepath.Join(os.TempDir(), "herdr-review-loop-index-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("temporary index files leaked: %v", after)
	}
}
