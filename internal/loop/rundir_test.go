package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openRunDir opens a run directory and registers its close, so the tests below read as the
// sequence of operations they are checking rather than as error handling.
func openRunDir(t *testing.T, repository string) *RunDir {
	t.Helper()
	dir, err := OpenRunDir(repository)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	return dir
}

func TestPrepareCreatesTheRunDirectoryAndClearsAStaleOne(t *testing.T) {
	repository := t.TempDir()
	stale := filepath.Join(repository, RunSubdir, "round-01")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := openRunDir(t, repository)
	if err := dir.Prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a crashed run's round survived: %v", err)
	}
	info, err := os.Stat(filepath.Join(repository, RunSubdir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("run directory mode %v", info.Mode().Perm())
	}
}

func TestOpenRunDirRefusesANonDirectoryAtEveryComponent(t *testing.T) {
	for _, test := range []struct {
		name    string
		plant   func(t *testing.T, repository string)
		mention string
	}{
		{"a regular file at the plugin directory", func(t *testing.T, repository string) {
			if err := os.WriteFile(filepath.Join(repository, PluginDir), []byte("no"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, PluginDir},
		{"a symlink at the run directory", func(t *testing.T, repository string) {
			if err := os.MkdirAll(filepath.Join(repository, PluginDir), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(repository, RunSubdir)); err != nil {
				t.Fatal(err)
			}
		}, RunSubdir},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := t.TempDir()
			test.plant(t, repository)
			_, err := OpenRunDir(repository)
			if err == nil || !strings.Contains(err.Error(), test.mention) {
				t.Fatalf("got %v, want an error naming %s", err, test.mention)
			}
		})
	}
}

func TestARoundRefusesAComponentPlantedAfterPreflight(t *testing.T) {
	repository := t.TempDir()
	dir := openRunDir(t, repository)
	if err := dir.Prepare(); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(repository, RunSubdir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repository, RunSubdir)); err != nil {
		t.Fatal(err)
	}
	if err := dir.Round(1).Create(); err == nil || !strings.Contains(err.Error(), RunSubdir) {
		t.Fatalf("got %v, want an error naming %s", err, RunSubdir)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the loop wrote outside the repository: %v %v", entries, err)
	}
}

func TestARoundDirectoryMustNotAlreadyExist(t *testing.T) {
	repository := t.TempDir()
	dir := openRunDir(t, repository)
	if err := dir.Prepare(); err != nil {
		t.Fatal(err)
	}
	round := dir.Round(1)
	if err := round.Create(); err != nil {
		t.Fatal(err)
	}
	if err := round.Create(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v, want an already-exists error", err)
	}
	if err := round.WriteReview([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := round.Reset(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := round.ReadReview(); err != nil || found {
		t.Fatalf("a retry inherited the previous attempt's file: found=%v err=%v", found, err)
	}
}

func readExclude(t *testing.T, repository string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repository, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExcludeRunHidesTheRunDirectoryFromGit(t *testing.T) {
	repository := newRepository(t)
	ctx := context.Background()
	if err := ExcludeRun(ctx, repository); err != nil {
		t.Fatal(err)
	}
	// a second run must not append the line twice
	if err := ExcludeRun(ctx, repository); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(readExclude(t, repository), ExcludeLine); got != 1 {
		t.Fatalf("exclude line appears %d times:\n%s", got, readExclude(t, repository))
	}
	dir := openRunDir(t, repository)
	if err := dir.Prepare(); err != nil {
		t.Fatal(err)
	}
	if err := dir.Round(1).Create(); err != nil {
		t.Fatal(err)
	}
	if err := dir.Round(1).WriteReview([]byte(`{"status":"clean"}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "code.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, statusErr := gitOutput(ctx, repository, "status", "--porcelain")
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if strings.Contains(status, PluginDir) {
		t.Fatalf("the run directory is in git status:\n%s", status)
	}
	if !strings.Contains(status, "code.go") {
		t.Fatalf("the author's change is missing from git status:\n%s", status)
	}
}

func TestTrackedRunFilesReportsWhatExcludeCannotHide(t *testing.T) {
	repository := newRepository(t)
	ctx := context.Background()
	if got := TrackedRunFiles(ctx, repository); len(got) != 0 {
		t.Fatalf("a fresh repository reports tracked run files: %#v", got)
	}
	if err := os.MkdirAll(filepath.Join(repository, RunSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, RunSubdir, "leftover.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(ctx, repository, "add", "-f", RunSubdir); err != nil {
		t.Fatal(err)
	}
	got := TrackedRunFiles(ctx, repository)
	if len(got) != 1 || !strings.Contains(got[0], "leftover.json") {
		t.Fatalf("tracked %#v", got)
	}
}

func TestExcludeRunIsHarmlessOutsideAWorkTree(t *testing.T) {
	if err := ExcludeRun(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("got %v, want no error outside a work tree", err)
	}
}
