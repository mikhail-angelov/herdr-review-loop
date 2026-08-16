package loop

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Verdict is what a review round concluded.
type Verdict int

const (
	// Findings means the reviewer wants changes. It is the zero value, so an unreadable or
	// unrecognized review never ends the loop by accident.
	Findings Verdict = iota
	// Clean means the reviewer had nothing left to raise.
	Clean
)

var status = regexp.MustCompile(`(?i)^STATUS:\s*(CLEAN|FINDINGS)$`)

const fileTimestampPrecision = time.Second

// SummaryFile is where the reviewer records the run-level summary, alongside the review file.
const SummaryFile = "review-summary.md"

// ParseVerdict reads the reviewer's STATUS line. Anything it cannot read counts as findings, so a
// malformed review keeps the loop going instead of declaring success.
func ParseVerdict(contents string) Verdict {
	for _, line := range strings.Split(contents, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if match := status.FindStringSubmatch(line); len(match) == 2 && strings.EqualFold(match[1], "CLEAN") {
				return Clean
			}
			return Findings
		}
	}
	return Findings
}

// ReviewFile is the reviewer's output file, held open through an os.Root so a symlink or a `..`
// in the configured path cannot reach outside the repository.
type ReviewFile struct {
	root               *os.Root
	Relative, Absolute string
}

// OpenReviewFile validates the configured path and opens the repository root that contains it.
// The returned file must be closed.
func OpenReviewFile(repository, configured string) (*ReviewFile, error) {
	if filepath.IsAbs(configured) {
		return nil, errors.New("review_file must be relative to the repository")
	}
	relative := filepath.Clean(configured)
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("review_file must remain inside the repository")
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository %s: %w", repository, err)
	}
	file := &ReviewFile{root: root, Relative: relative, Absolute: filepath.Join(repository, relative)}
	if info, err := root.Lstat(relative); err == nil && !info.Mode().IsRegular() {
		_ = root.Close()
		return nil, errors.New("review_file must name a regular file")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = root.Close()
		return nil, fmt.Errorf("review_file: %w", err)
	}
	return file, nil
}

// OpenSummaryFile opens the run summary next to the review file.
func OpenSummaryFile(repository string) (*ReviewFile, error) {
	return OpenReviewFile(repository, SummaryFile)
}

// Close releases the repository root.
func (f *ReviewFile) Close() error {
	if err := f.root.Close(); err != nil {
		return fmt.Errorf("failed to close repository root: %w", err)
	}
	return nil
}

// Remove deletes the file; a file that is already gone is not an error.
func (f *ReviewFile) Remove() error {
	err := f.root.Remove(f.Relative)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("failed to remove %s: %w", f.Relative, err)
}

// Exists reports whether the file is present.
func (f *ReviewFile) Exists() (bool, error) {
	_, err := f.root.Lstat(f.Relative)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stat %s: %w", f.Relative, err)
	}
	return true, nil
}

// EnsureParent creates the directories leading to the file.
func (f *ReviewFile) EnsureParent() error {
	parent := filepath.Dir(f.Relative)
	if parent == "." {
		return nil
	}
	if err := f.root.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", parent, err)
	}
	return nil
}

// Read returns the file's contents together with the stat it was read from, so callers compare
// one consistent pair rather than stat-ing again.
func (f *ReviewFile) Read() (contents string, info fs.FileInfo, err error) {
	info, err = f.root.Lstat(f.Relative)
	if err != nil {
		return "", nil, fmt.Errorf("failed to stat %s: %w", f.Relative, err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("review_file is not a regular file")
	}
	raw, err := f.root.ReadFile(f.Relative)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read %s: %w", f.Relative, err)
	}
	return string(raw), info, nil
}

// WrittenSince reports the file's contents and whether it was written at or after askedAt, which
// is how a fresh review is told apart from the previous round's leftovers.
func (f *ReviewFile) WrittenSince(askedAt time.Time) (contents string, fresh bool, err error) {
	contents, info, err := f.Read()
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return contents, !info.ModTime().Before(askedAt.Truncate(fileTimestampPrecision)), nil
}

// EnsureRegular fails early when the configured path exists but is not a readable regular file.
func (f *ReviewFile) EnsureRegular() error {
	_, _, err := f.Read()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// WaitForChange blocks until the file gains non-empty content written at or after askedAt.
func (f *ReviewFile) WaitForChange(ctx context.Context, askedAt time.Time, timeout time.Duration) (string, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		contents, fresh, err := f.WrittenSince(askedAt)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if err == nil && fresh && strings.TrimSpace(contents) != "" {
			return contents, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("phase ended: %w", ctx.Err())
		case <-deadline.C:
			return "", errors.New("was not updated")
		case <-tick.C:
		}
	}
}

// WaitForVerdict blocks until a fresh review lands and returns it with the verdict it carries.
func (f *ReviewFile) WaitForVerdict(ctx context.Context, askedAt time.Time, timeout time.Duration) (string, Verdict, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		contents, fresh, err := f.WrittenSince(askedAt)
		if err != nil {
			return "", Findings, err
		}
		if fresh {
			return contents, ParseVerdict(contents), nil
		}
		select {
		case <-ctx.Done():
			return "", Findings, fmt.Errorf("review phase ended: %w", ctx.Err())
		case <-deadline.C:
			return "", Findings, fmt.Errorf("reviewer settled without writing %s", f.Relative)
		case <-tick.C:
		}
	}
}
