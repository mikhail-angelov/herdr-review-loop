package loop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Verdict int

const (
	Findings Verdict = iota
	Clean
)

var status = regexp.MustCompile(`(?i)^STATUS:\s*(CLEAN|FINDINGS)$`)

func ParseVerdict(contents string) Verdict {
	for _, line := range strings.Split(contents, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if match := status.FindStringSubmatch(line); match != nil && strings.EqualFold(match[1], "CLEAN") {
				return Clean
			}
			return Findings
		}
	}
	return Findings
}

type ReviewFile struct {
	root               *os.Root
	Relative, Absolute string
}

func OpenReviewFile(repository, configured string) (*ReviewFile, error) {
	if filepath.IsAbs(configured) {
		return nil, fmt.Errorf("review_file must be relative to the repository")
	}
	relative := filepath.Clean(configured)
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("review_file must remain inside the repository")
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return nil, err
	}
	file := &ReviewFile{root: root, Relative: relative, Absolute: filepath.Join(repository, relative)}
	if info, err := root.Lstat(relative); err == nil && !info.Mode().IsRegular() {
		_ = root.Close()
		return nil, fmt.Errorf("review_file must name a regular file")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = root.Close()
		return nil, fmt.Errorf("review_file: %w", err)
	}
	return file, nil
}

func (f *ReviewFile) Close() error { return f.root.Close() }
func (f *ReviewFile) Remove() error {
	err := f.root.Remove(f.Relative)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
func (f *ReviewFile) EnsureParent() error {
	parent := filepath.Dir(f.Relative)
	if parent == "." {
		return nil
	}
	return f.root.MkdirAll(parent, 0o755)
}
func (f *ReviewFile) Read() (string, fs.FileInfo, error) {
	info, err := f.root.Lstat(f.Relative)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("review_file is not a regular file")
	}
	contents, err := f.root.ReadFile(f.Relative)
	return string(contents), info, err
}
func (f *ReviewFile) WrittenSince(askedAt time.Time) (string, bool, error) {
	contents, info, err := f.Read()
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return contents, !info.ModTime().Before(askedAt), nil
}
func (f *ReviewFile) WaitForVerdict(ctxDone <-chan struct{}, askedAt time.Time, timeout time.Duration) (string, Verdict, error) {
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
		case <-ctxDone:
			return "", Findings, fmt.Errorf("review phase cancelled")
		case <-deadline.C:
			return "", Findings, fmt.Errorf("reviewer settled without writing %s", f.Relative)
		case <-tick.C:
		}
	}
}
