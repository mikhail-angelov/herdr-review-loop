package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
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

// fakeHerdr is a herdr that records every invocation and answers a pane open with a pane id, so a
// test can say not only that a pane was opened but that the user was taken to it.
func fakeHerdr(t *testing.T, log string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	body := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >>" + log + "\n" +
		"if [[ \"$*\" == *'pane open'* ]]; then printf '%s\\n' '{\"result\":{\"plugin_pane\":{\"pane\":{\"pane_id\":\"pane-7\",\"tab_id\":\"tab-9\"}}}}'; else printf '%s\\n' '{\"result\":{}}'; fi\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOpenPaneTakesTheUserToTheTabItOpened(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls")
	client := herdr.Client{Binary: fakeHerdr(t, log)}
	var environment herdr.Environment
	environment.Context.FocusedPaneID, environment.Context.WorkspaceID = "pane-1", "ws-1"

	for _, entrypoint := range []string{"findings", "diff"} {
		if err := openPane(t.Context(), client, environment, entrypoint); err != nil {
			t.Fatalf("%s: %v", entrypoint, err)
		}
	}
	// the panel is the one pane nobody asked for, so it must not move the user
	if err := openPane(t.Context(), client, environment, "panel"); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	text := string(calls)
	// a pane in a tab of its own is reached by focusing the tab: the terminal follows the tab it
	// was told to show, and a pane focused inside a tab nobody switched to leaves the user put
	if got := strings.Count(text, "tab focus tab-9"); got != 2 {
		t.Fatalf("findings and diff focused their tab %d time(s), want 2:\n%s", got, text)
	}
	if strings.Contains(text, "plugin pane focus pane-7") {
		t.Fatalf("a tab pane was focused by pane instead of by tab:\n%s", text)
	}
	if !strings.Contains(text, "--placement tab") || !strings.Contains(text, "--workspace ws-1") {
		t.Fatalf("the tabs were not opened as tabs in the workspace:\n%s", text)
	}
	if strings.Contains(text, "entrypoint panel --placement split --direction right --target-pane pane-1 --focus") {
		t.Fatalf("the panel took focus:\n%s", text)
	}
}
