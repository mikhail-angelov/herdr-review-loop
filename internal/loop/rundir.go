package loop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PluginDir is the directory a project commits its review-loop configuration to. The loop's own
// run artifacts live in a subdirectory of it, so one name covers both and a project can ignore or
// commit the parts it wants.
const PluginDir = ".review-loop"

// RunSubdir is the active run's directory, relative to the repository. It is excluded from git so
// nothing the loop writes ever reaches the diff the reviewer reads.
const RunSubdir = PluginDir + "/run"

// ExcludeLine is what the loop appends to .git/info/exclude. The leading slash anchors the pattern
// to the repository root, and the trailing one restricts it to a directory.
const ExcludeLine = "/" + RunSubdir + "/"

// reviewFile and decisionsFile are the two names a round exchanges through.
const (
	reviewFile    = "review.json"
	decisionsFile = "decisions.json"
)

// RunDir is the loop's private directory inside the repository, reached only through an os.Root
// opened on the repository. Rooted access is kept even though the plugin owns the path now:
// owning a name is not owning what it points at, and every path here is handed to an agent as an
// absolute path, so a symlinked component would redirect the agent's writes as well as the loop's.
type RunDir struct {
	root       *os.Root
	Repository string
}

// OpenRunDir opens the repository and checks every component of the run path. The returned
// directory must be closed.
func OpenRunDir(repository string) (*RunDir, error) {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository %s: %w", repository, err)
	}
	dir := &RunDir{root: root, Repository: repository}
	if err := dir.verify(PluginDir, RunSubdir); err != nil {
		_ = root.Close()
		return nil, err
	}
	return dir, nil
}

// Close releases the repository root.
func (d *RunDir) Close() error {
	if err := d.root.Close(); err != nil {
		return fmt.Errorf("failed to close repository root: %w", err)
	}
	return nil
}

// Absolute is the path handed to an agent, which needs one it can write from any working
// directory. It is inside the repository, so a workspace sandbox still permits the write.
func (d *RunDir) Absolute(relative string) string {
	return filepath.Join(d.Repository, filepath.FromSlash(relative))
}

// verify requires every named component to be absent or a real directory. A symlink fails even
// when it points somewhere valid, because os.Root follows links that stay inside the root and a
// planted one would move the whole run directory without any individual path looking wrong.
func (d *RunDir) verify(components ...string) error {
	for _, component := range components {
		info, err := d.root.Lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to inspect %s: %w", component, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s must be a directory, not %s", component, describeMode(info.Mode()))
		}
	}
	return nil
}

func describeMode(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeSymlink != 0:
		return "a symbolic link"
	case mode.IsRegular():
		return "a regular file"
	default:
		return "a " + mode.Type().String()
	}
}

// Prepare clears anything a crashed run left behind and creates the run directory. It runs once
// the lock is held, so a stale directory is never one the loop writes into blind.
func (d *RunDir) Prepare() error {
	if err := d.verify(PluginDir, RunSubdir); err != nil {
		return err
	}
	if err := d.root.RemoveAll(RunSubdir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to clear %s: %w", RunSubdir, err)
	}
	if err := d.root.MkdirAll(RunSubdir, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", RunSubdir, err)
	}
	return nil
}

// Remove deletes the run directory, which is all finish has to clean out of the working tree.
func (d *RunDir) Remove() error {
	if err := d.root.RemoveAll(RunSubdir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove %s: %w", RunSubdir, err)
	}
	return nil
}

// Exists reports whether the run directory is present, so finish can say it found nothing.
func (d *RunDir) Exists() (bool, error) {
	_, err := d.root.Lstat(RunSubdir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to inspect %s: %w", RunSubdir, err)
	}
	return true, nil
}

// Round is one round's directory. The review phase creates it, which is what makes the presence
// of review.json proof that this round's reviewer wrote it.
type Round struct {
	dir      *RunDir
	Number   int
	relative string
}

// Round names a round's directory without creating it.
func (d *RunDir) Round(number int) *Round {
	return &Round{dir: d, Number: number, relative: fmt.Sprintf("%s/round-%02d", RunSubdir, number)}
}

// Create makes the round directory, which must not exist yet: an existing one would carry a
// previous attempt's files and destroy the freshness the loop reads from mere presence.
func (r *Round) Create() error {
	if err := r.dir.verify(PluginDir, RunSubdir); err != nil {
		return err
	}
	if err := r.dir.root.Mkdir(r.relative, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", r.relative)
		}
		return fmt.Errorf("failed to create %s: %w", r.relative, err)
	}
	return nil
}

// Reset discards a failed attempt's directory and makes a fresh one, so a retry starts from the
// same blank state the first attempt did.
func (r *Round) Reset() error {
	if err := r.dir.verify(PluginDir, RunSubdir); err != nil {
		return err
	}
	if err := r.dir.root.RemoveAll(r.relative); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to clear %s: %w", r.relative, err)
	}
	return r.Create()
}

// ReviewPath is the absolute path the reviewer is told to write.
func (r *Round) ReviewPath() string { return r.dir.Absolute(r.relative + "/" + reviewFile) }

// DecisionsPath is the absolute path the author is told to write.
func (r *Round) DecisionsPath() string { return r.dir.Absolute(r.relative + "/" + decisionsFile) }

// ReadReview returns the reviewer's output verbatim. A file that is not there yet reports false
// rather than an error: the caller is polling for it.
func (r *Round) ReadReview() (contents string, found bool, err error) { return r.read(reviewFile) }

// ReadDecisions returns the author's output verbatim, on the same terms.
func (r *Round) ReadDecisions() (contents string, found bool, err error) {
	return r.read(decisionsFile)
}

// WriteReview replaces the reviewer's file with the loop's parsed form of it, which is how the
// author gets findings that carry the ids it has to decide on.
func (r *Round) WriteReview(contents []byte) error { return r.write(reviewFile, contents) }

// RemoveDecisions clears the author's file before its phase starts, so its absence is what proves
// the file that appears afterwards belongs to this attempt.
func (r *Round) RemoveDecisions() error {
	if err := r.dir.verify(PluginDir, RunSubdir, r.relative); err != nil {
		return err
	}
	path := r.relative + "/" + decisionsFile
	if err := r.dir.root.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to remove %s: %w", path, err)
	}
	return nil
}

func (r *Round) read(name string) (contents string, found bool, err error) {
	path := r.relative + "/" + name
	if verifyErr := r.dir.verify(PluginDir, RunSubdir, r.relative); verifyErr != nil {
		return "", false, verifyErr
	}
	info, err := r.dir.root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("failed to inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("%s must be a regular file, not %s", path, describeMode(info.Mode()))
	}
	data, err := r.dir.root.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return string(data), true, nil
}

func (r *Round) write(name string, contents []byte) error {
	path := r.relative + "/" + name
	if err := r.dir.verify(PluginDir, RunSubdir, r.relative); err != nil {
		return err
	}
	if err := r.dir.root.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// ExcludeRun makes git ignore the run directory for this clone alone. info/exclude is untracked
// and per-clone, so no file the user owns or commits is modified. Outside a work tree there is
// nothing to exclude and nothing to hide from, and the call succeeds having done nothing.
func ExcludeRun(ctx context.Context, repository string) error {
	common, err := gitOutput(ctx, repository, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repository, common)
	}
	info := filepath.Join(common, "info")
	// git's own directory, whose mode is git's business rather than this plugin's
	if mkdirErr := os.MkdirAll(info, 0o750); mkdirErr != nil {
		return fmt.Errorf("failed to create %s: %w", info, mkdirErr)
	}
	path := filepath.Join(info, "exclude")
	existing, err := os.ReadFile(path) //nolint:gosec // the path comes from git's own answer for this repository
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("failed to read %s: %w", path, err)
	}
	for line := range strings.SplitSeq(string(existing), "\n") {
		if strings.TrimSpace(line) == ExcludeLine {
			return nil
		}
	}
	addition := ExcludeLine + "\n"
	if len(existing) > 0 && !bytes.HasSuffix(existing, []byte("\n")) {
		addition = "\n" + addition
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // git's own exclude file, which git expects to be readable
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	_, writeErr := file.WriteString(addition)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("failed to write %s: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close %s: %w", path, closeErr)
	}
	return nil
}

// TrackedRunFiles lists paths under the run directory that git already tracks. info/exclude does
// not ignore what is already tracked, so such a repository would keep staging the run's files into
// every checkpoint and into the reviewed diff while the exclude line sat there looking effective.
func TrackedRunFiles(ctx context.Context, repository string) []string {
	out, err := gitOutput(ctx, repository, "ls-files", "-z", "--", RunSubdir)
	if err != nil {
		return nil
	}
	var tracked []string
	for name := range strings.SplitSeq(out, "\x00") {
		if name != "" {
			tracked = append(tracked, name)
		}
	}
	return tracked
}

func gitOutput(ctx context.Context, repository string, args ...string) (string, error) {
	//nolint:gosec // git and its subcommands are literals here; only the repository path varies
	command := exec.CommandContext(ctx, "git", append([]string{"-C", repository}, args...)...)
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
