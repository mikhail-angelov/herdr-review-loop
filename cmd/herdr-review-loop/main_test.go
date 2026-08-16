package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/loop"
)

func TestRunBuiltInCommandsAndUsageErrors(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"version", "extra"}, {"review", "--unknown"}, {"unknown"}} {
		if err := run(args); !errors.Is(err, errUsage) {
			t.Errorf("run(%q) = %v, want usage error", args, err)
		}
	}
}

func TestHistoryRoundRejectsMissingRounds(t *testing.T) {
	views := []loop.RunView{{}}
	if _, ok := historyRun(views, 1); ok {
		t.Fatal("out-of-range history run exists")
	}
	if _, ok := historyRound(views, 0, 0); ok {
		t.Fatal("empty history run has a round")
	}
}

func TestPagerFallsBackWhenPagerIsWhitespace(t *testing.T) {
	t.Setenv("PAGER", " \t ")
	command := pager("review.md")
	if command == nil || len(command.Args) == 0 || command.Args[len(command.Args)-1] != "review.md" {
		t.Fatalf("pager(%q) = %#v", "review.md", command)
	}
	if command.Path == "" {
		t.Fatal("pager command is empty")
	}
}

func TestRunTitleIdentifiesTheRepository(t *testing.T) {
	title := runTitle(loop.RunView{Record: loop.RunRecord{
		Repository: "/work/repository-a",
		Started:    time.Now(),
	}})
	if !strings.Contains(title, "/work/repository-a") {
		t.Fatalf("run title does not identify its repository: %q", title)
	}
}
