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

func TestPagerFeedsRenderedTextAndFallsBackWhenPagerIsWhitespace(t *testing.T) {
	t.Setenv("PAGER", " \t ")
	command := pager("# run · round 1\n")
	if command == nil || command.Path == "" {
		t.Fatalf("pager returned %#v", command)
	}
	if command.Stdin == nil {
		t.Fatal("the pager was given no text to show")
	}
	if pager("") != nil {
		t.Fatal("an empty round produced a pager command")
	}
}

func TestShowArgumentsDefaultsToTheLastRoundInMarkdown(t *testing.T) {
	runID, round, format, err := showArguments(nil)
	if err != nil || runID != "" || round != 0 || format != "md" {
		t.Fatalf("showArguments(nil) = %q %d %q %v", runID, round, format, err)
	}
	runID, round, format, err = showArguments([]string{"--run", "r", "--round", "2", "--format", "json"})
	if err != nil || runID != "r" || round != 2 || format != "json" {
		t.Fatalf("showArguments = %q %d %q %v", runID, round, format, err)
	}
	for _, args := range [][]string{{"--round", "0"}, {"--format", "html"}, {"--run"}, {"--nope", "x"}} {
		if _, _, _, err := showArguments(args); !errors.Is(err, errUsage) {
			t.Errorf("showArguments(%q) = %v, want a usage error", args, err)
		}
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
