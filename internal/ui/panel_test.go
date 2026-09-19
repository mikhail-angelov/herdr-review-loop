package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

var styling = regexp.MustCompile("\x1b\\[[0-9;]*m")

// plain is the pane with its styling taken off, so a test can assert on what a pane says rather
// than on how it says it.
func plain(view string) string { return styling.ReplaceAllString(view, "") }

func TestPanelViewClipsAndKeepsHints(t *testing.T) {
	for _, width := range []int{32, 44, 80} {
		view := PanelView(PanelState{Author: "claude @ w:p1", Reviewer: "codex @ w:p2", Events: []FeedLine{{Text: "a very long log line that must be clipped"}}}, width, 8)
		if !strings.Contains(plain(view), "r review") || !strings.Contains(plain(view), "q close") {
			t.Fatalf("width %d: unexpected view %q", width, view)
		}
		if !strings.Contains(view, "\x1b[0m") {
			t.Fatalf("width %d: header reset missing: %q", width, view)
		}
	}
}

func TestPanelViewShowsOneShotChoicesWhenItIsWaiting(t *testing.T) {
	view := plain(PanelView(PanelState{Running: true, Pending: PendingOneShot}, 80, 12))
	for _, hint := range []string{"a apply", "c cancel", "i instruction"} {
		if !strings.Contains(view, hint) {
			t.Fatalf("missing %q in %q", hint, view)
		}
	}
}

func TestPanelKeysSubmitAOneShotChoiceOrCustomInstruction(t *testing.T) {
	actions := PanelActions{Answer: func(action, text string) string {
		if text == "" {
			return action
		}
		return action + ": " + text
	}}
	state := PanelState{Pending: PendingOneShot}
	pane := panelKey("a", state, actions, panelUI{})
	if pane.message != "apply" || pane.input != nil || pane.exit {
		t.Fatalf("apply = %#v", pane)
	}
	pane = panelKey("i", state, actions, panelUI{})
	if pane.message != "" || pane.input == nil || pane.input.value != "" {
		t.Fatalf("instruction start = %#v", pane)
	}
	pane = panelKey("x", state, actions, pane)
	if pane.input == nil || pane.input.value != "x" {
		t.Fatalf("instruction input = %#v", pane)
	}
	pane = panelKey("\n", state, actions, pane)
	if pane.message != "instruct: x" || pane.input != nil {
		t.Fatalf("instruction submit = %#v", pane)
	}
}

// The scope is a setting, so picking one writes it; auto is one of the choices, which is how a
// decision made for you is handed back to the loop.
func TestScopePickerChoosesASpecOrAsksForOne(t *testing.T) {
	options := []ScopeOption{{Label: "auto", Spec: "auto", Current: true}, {Label: "branch", Spec: "branch"}, {Label: "type one"}}
	chosen := ""
	actions := PanelActions{
		Scopes:   func() []ScopeOption { return options },
		SetScope: func(spec string) string { chosen = spec; return "scope → " + spec },
	}
	pane := panelKey("e", PanelState{}, actions, panelUI{})
	if !pane.picking || pane.picked != 0 {
		t.Fatalf("the picker did not open on the current scope: %#v", pane)
	}
	open := PanelState{Picker: options, Picked: pane.picked}
	if view := plain(PanelView(open, 60, 14)); !strings.Contains(view, "← current") || !strings.Contains(view, "enter choose") {
		t.Fatalf("the open picker does not say what it is: %q", view)
	}
	pane = panelKey("j", open, actions, pane)
	pane = panelKey("\r", open, actions, pane)
	if chosen != "branch" || pane.picking || pane.message != "scope → branch" {
		t.Fatalf("choosing branch: %q %#v", chosen, pane)
	}
	// the row that takes an argument opens the input instead of setting anything
	pane = panelKey("e", PanelState{}, actions, panelUI{})
	pane.picked = 2
	pane = panelKey("enter", open, actions, pane)
	if pane.picking || pane.input == nil || pane.input.label != "scope" {
		t.Fatalf("the typed scope did not open an input: %#v", pane)
	}
	pane.input.value = "branch:origin/main"
	pane = panelKey("\r", open, actions, pane)
	if chosen != "branch:origin/main" || pane.input != nil {
		t.Fatalf("typed scope: %q %#v", chosen, pane)
	}
}

func TestPanelViewFitsAvailableRowsWithoutWrapping(t *testing.T) {
	view := PanelView(PanelState{
		Events:  []FeedLine{{Text: "a log line that is deliberately much wider than the panel"}},
		Message: "a status message that is deliberately much wider than the panel",
	}, 32, 8)
	if got := len(strings.Split(view, "\n")); got > 8 {
		t.Fatalf("panel uses %d rows, want at most 8: %q", got, view)
	}
	if strings.Contains(plain(view), "\nwide than the panel") {
		t.Fatalf("panel wrapped a long line: %q", view)
	}
	for _, line := range strings.Split(plain(view), "\n") {
		if got := len([]rune(line)); got > 32 {
			t.Fatalf("a %d-column line in a 32-column panel: %q", got, line)
		}
	}
}

// The colors are the panel's answer to "what is happening right now" at a glance, so each state
// has to be told from the others without reading the words.
func TestPanelColorsStatusesAndEventsByWhatTheyMean(t *testing.T) {
	state := PanelState{
		Author: "claude @ w:p1", AuthorStatus: "working",
		Reviewer: "codex @ w:p2", ReviewerStatus: "blocked",
		Running: true,
		Events: []FeedLine{
			{Text: "round 1 review: stall — no output", Level: LevelBad},
			{Text: "round 1 review: filtered — 2 below confirmed", Level: LevelWarn},
			{Text: "round 1 review: phase_done — 3 finding(s)", Level: LevelGood},
		},
	}
	view := PanelView(state, 80, 16)
	for _, want := range []string{Cyan("working"), Red("blocked"), Green("● running"), Red("round 1 review: stall — no output")} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in\n%s", want, view)
		}
	}
	if !strings.Contains(view, Bold("p")+" "+Dim("pause")) {
		t.Fatalf("the keys do not stand out from what they do:\n%s", view)
	}
	// a pane too narrow for both keeps the agent and drops its state rather than clipping it
	narrow := plain(PanelView(state, 20, 16))
	if strings.Contains(narrow, "worki") {
		t.Fatalf("a clipped status survived a narrow panel:\n%s", narrow)
	}
}

func TestSettingsViewShowsRunStatusAndDimsDefaults(t *testing.T) {
	values := config.Defaults()
	values.MaxIterations = 2
	pane := &settingsPane{directory: "/tmp/config", fields: config.Fields(), values: values, message: "message"}
	view := pane.view("review loop running (pid 42, since 12:34)", 100, 40)
	if !strings.Contains(view, "review loop running (pid 42, since 12:34)") {
		t.Fatalf("missing run status: %q", view)
	}
	if !strings.Contains(view, "\x1b[2m30m\x1b[0m") {
		t.Fatalf("default values are not dimmed: %q", view)
	}
	if strings.Contains(view, "\x1b[2m2\x1b[0m") {
		t.Fatalf("non-default value is dimmed: %q", view)
	}
	pane.message, pane.editing, pane.input = "enter accept", true, "claude"
	editing := pane.view("", 100, 40)
	if !strings.Contains(editing, "reviewer kind") || !strings.Contains(editing, "claude") {
		t.Fatalf("missing edit state: %q", editing)
	}
}

func TestSettingsViewMatchesNodePopupLayout(t *testing.T) {
	pane := &settingsPane{directory: "/tmp/config", fields: config.Fields(), values: config.Defaults(), message: "saved"}
	view := pane.view("", 80, 40)
	if !strings.HasPrefix(view, "\n  ") {
		t.Fatalf("settings should begin with the Node popup's padded header: %q", view)
	}
	if !strings.Contains(plain(view), config.Fields()[0].Hint) || !strings.Contains(plain(view), "j/k move · enter edit") {
		t.Fatalf("settings should keep the selected-field hint and key help visible: %q", view)
	}
}

func TestSettingsViewDoesNotRepeatKeyHelpInMessage(t *testing.T) {
	keys := "j/k move · enter edit · d default · s save · x cancel run · q close"
	pane := &settingsPane{directory: "/tmp/config", fields: config.Fields(), values: config.Defaults(), message: keys}
	view := plain(pane.view("", 80, 40))
	if got := strings.Count(view, keys); got != 1 {
		t.Fatalf("key help appears %d times, want once: %q", got, view)
	}
}

func TestPanelOffersNoReviewWhileAnotherSpaceHoldsTheLoop(t *testing.T) {
	state := PanelState{Busy: true}
	view := plain(PanelView(state, 80, 12))
	if strings.Contains(view, "r review") || strings.Contains(view, "o one-shot") {
		t.Fatalf("a busy panel offered to start a review: %q", view)
	}
	actions := PanelActions{Review: func() string { return "started" }, OneShot: func() string { return "started" }}
	if pane := panelKey("r", state, actions, panelUI{}); pane.message == "started" {
		t.Fatal("r started a review the lock would have refused")
	}
}
