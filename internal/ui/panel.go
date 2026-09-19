package ui

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// PanelState is everything the panel renders: who is paired with whom, what the loop is doing, and
// the run's event stream it scrolls.
type PanelState struct {
	Author, Reviewer, Phase, Message string
	// AuthorStatus and ReviewerStatus are Herdr's own words for what each agent is doing — idle,
	// working, blocked — kept apart from the names so the panel can color them.
	AuthorStatus, ReviewerStatus string
	Events                       []FeedLine
	Running                      bool
	// Scope is what a run started here would review, and Changes how much that covers. They are
	// shown before a run rather than after it, because the scope is the one setting whose wrong
	// value cannot be told from a clean review.
	Scope   string
	Changes string
	// Pending names the boundary the loop is waiting at, empty when it is not waiting. Paused is
	// the request that produces one: asked for at any moment, honored at the next boundary.
	Pending string
	Paused  bool
	Summary string
	// Busy is a loop running in another space. One runs at a time, so this panel can show its own
	// history and nothing else: offering to start a review that the lock would refuse is worse
	// than not offering one.
	Busy bool
	// Picker is an open list the user is choosing from, and Picked the row the marker sits on. It
	// takes the place of the event feed while it is open: choosing is a moment, and the feed is
	// still there afterwards.
	Picker []ScopeOption
	Picked int
}

// ScopeOption is one row of the scope picker: what it says, what it sets, and whether it is what a
// run started here would use now. An option with no Spec is the one that asks for a scope to be
// typed, which is how the two scopes that take an argument are reached.
type ScopeOption struct {
	Label   string
	Spec    string
	Current bool
}

// FeedLine is one event as the panel shows it: the text, and how much it should stand out. The
// level is the panel's own vocabulary, so the loop keeps naming events and the panel keeps
// deciding what red means.
type FeedLine struct {
	Text  string
	Level string
}

// The levels a feed line can carry. The empty one is the ordinary course of a run.
const (
	LevelNormal = ""
	LevelGood   = "good"
	LevelWarn   = "warn"
	LevelBad    = "bad"
)

// The pending kinds the panel offers choices for. They are the loop's own names for its
// boundaries; the panel only decides which keys each one is answered with.
const (
	PendingOneShot = "one-shot"
	PendingGate    = "gate"
)

// PanelView renders one frame of the panel into width columns and rows lines.
func PanelView(state PanelState, width, rows int) string {
	if width < 1 || rows < 1 {
		return ""
	}
	header := []string{titleLine(state, width), agentLine("author   ", state.Author, state.AuthorStatus, width), agentLine("review by ", state.Reviewer, state.ReviewerStatus, width)}
	if state.Scope != "" {
		header = append(header, labeled("scope    ", state.Scope, width))
	}
	if state.Changes != "" {
		header = append(header, labeled("changes  ", state.Changes, width))
	}
	header = append(header, Clip(state.Phase, width))
	if state.Summary != "" {
		header = append(header, Clip(state.Summary, width))
	}
	header = append(header, strings.Repeat("─", width))
	// the hints are laid out against the rows left once the header is drawn, because a panel that
	// scrolled its own keys off the bottom would be a panel nobody can drive
	footer := append([]string{""}, panelHints(state, width, rows-len(header)-1)...)
	tail := make([]string, 0, len(state.Events))
	for _, line := range state.Events {
		text := line.Text
		if width < 44 && strings.HasPrefix(text, "[") {
			if end := strings.Index(text, "] "); end >= 0 {
				text = text[end+2:]
			}
		}
		tail = append(tail, levelStyle(line.Level)(Clip(text, width)))
	}
	if len(state.Picker) > 0 {
		tail = pickerLines(state, width)
	}
	budget := rows - len(header) - len(footer)
	if state.Message != "" {
		budget--
	}
	if budget < 0 {
		budget = 0
	}
	if len(tail) > budget {
		tail = tail[len(tail)-budget:]
	}
	lines := make([]string, 0, len(header)+len(tail)+len(footer)+1)
	lines = append(lines, header...)
	lines = append(lines, tail...)
	lines = append(lines, footer...)
	if state.Message != "" {
		lines = append(lines, Clip(state.Message, width))
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

// titleLine is the panel's own state, colored: green while a run is going, yellow while it is
// waiting for a person, dim when there is nothing to watch.
func titleLine(state PanelState, width int) string {
	status, style := "idle", Dim
	switch {
	case state.Pending != "":
		status, style = "⏸ waiting for you", Yellow
	case state.Running && state.Paused:
		status, style = "⏸ pausing", Yellow
	case state.Running:
		status, style = "● running", Green
	}
	const name = "herdr-review-loop  "
	line := Bold(Clip(name, width))
	if room := width - len([]rune(name)); room > 0 {
		line += style(Clip(status, room))
	}
	return line
}

// agentLine names an agent and colors what Herdr sees it doing. The status is dropped rather than
// clipped when the pane is too narrow for both: which agent it is matters more than its state, and
// a half-written "block" would read as neither.
func agentLine(label, name, status string, width int) string {
	if status == "" {
		return Clip(label+name, width)
	}
	suffix := " · " + status
	room := width - len([]rune(suffix))
	if room < 12 {
		return Clip(label+name, width)
	}
	return Clip(label+name, room) + " · " + statusStyle(status)(status)
}

// labeled dims the label and leaves the value alone, so a column of labels reads as a column.
func labeled(label, value string, width int) string {
	if room := width - len([]rune(label)); room > 0 {
		return Dim(label) + Clip(value, room)
	}
	return Dim(Clip(label, width))
}

// statusStyle is how an agent's state reads at a glance: green when it is ready for input, cyan
// while it is working, red when it is waiting on a person, dim when Herdr cannot tell.
func statusStyle(status string) func(string) string {
	switch status {
	case "idle", "done":
		return Green
	case "working":
		return Cyan
	case "blocked":
		return Red
	default:
		return Dim
	}
}

// levelStyle colors one event by how much it wants attention.
func levelStyle(level string) func(string) string {
	switch level {
	case LevelBad:
		return Red
	case LevelWarn:
		return Yellow
	case LevelGood:
		return Green
	default:
		return func(text string) string { return text }
	}
}

// pickerLines renders the open list where the event feed was. The rows are ordered by the caller,
// so what the panel offers and what the binary does with the answer stay one list.
func pickerLines(state PanelState, width int) []string {
	lines := make([]string, 0, len(state.Picker))
	for index, option := range state.Picker {
		label := option.Label
		if option.Current {
			label += "  ← current"
		}
		marker := "  "
		if index == state.Picked {
			marker = "▸ "
			label = Bold(label)
		}
		lines = append(lines, Clip(marker, width)+Clip(label, max(width-2, 1)))
	}
	return lines
}

// panelHints are the keys that do something in the state the panel is in. A key that would be
// refused is not offered: the panel is the only place these boundaries are explained.
func panelHints(state PanelState, width, rows int) []string {
	hints := []string{"r review", "o one-shot", "e scope", "f finish", "v findings", "d diff", "l log", "h history", "s settings", "q close"}
	switch {
	case len(state.Picker) > 0:
		hints = []string{"j/k move", "enter choose", "esc cancel"}
	case state.Busy && !state.Running:
		hints = []string{"v findings", "d diff", "l log", "h history", "s settings", "q close"}
	case state.Pending == PendingOneShot:
		hints = []string{"a apply", "c cancel", "i instruction", "v findings", "d diff", "l log", "x stop", "q close"}
	case state.Pending == PendingGate:
		hints = []string{"c continue", "n one round", "i instruction", "v findings", "d diff", "l log", "x stop", "q close"}
	case state.Running && state.Paused:
		hints = []string{"p resume", "x stop", "v findings", "d diff", "l log", "q close"}
	case state.Running:
		hints = []string{"p pause", "x stop", "v findings", "d diff", "l log", "s settings", "q close"}
	}
	rowsOfHints := wrapHints(hints, width)
	// a short pane keeps the first key and the one that closes the pane, and gives up the ones in
	// between: they are all reachable from Herdr's own action menu, and those two are not
	for len(rowsOfHints) > rows && len(hints) > 2 {
		hints = append(hints[:len(hints)-2], hints[len(hints)-1])
		rowsOfHints = wrapHints(hints, width)
	}
	return renderHints(rowsOfHints)
}

// wrapHints lays the hints out in plain text, because a line's width is counted in runes and a
// style is not one. What each row holds is returned rather than the row itself, so the caller can
// style the pieces after the arithmetic is done.
func wrapHints(hints []string, width int) [][]string {
	rows := [][]string{{}}
	length := 0
	for _, hint := range hints {
		last := len(rows) - 1
		width0 := len([]rune(hint))
		if len(rows[last]) > 0 {
			width0 += 3
		}
		if length+width0 <= width || len(rows[last]) == 0 {
			rows[last] = append(rows[last], hint)
			length += width0
			continue
		}
		rows = append(rows, []string{hint})
		length = len([]rune(hint))
	}
	return rows
}

// renderHints styles each hint as its key and what the key does, so the keys read as keys rather
// than as the first word of a sentence.
func renderHints(rows [][]string) []string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, Keys(strings.Join(row, " · ")))
	}
	return lines
}

// PanelActions are the panel's keys. Each returns the line to show as the result
// of the keypress; each re-invokes the binary's own subcommand, so starting,
// canceling and finishing a review exist in exactly one place.
// Finish reports whether the review was closed out. On success the panel has
// nothing left to show and exits, which ends its pane; on failure — a loop still
// running — the reason stays on screen instead of vanishing with the pane.
type PanelActions struct {
	Review, OneShot, Stop, Settings, History, Findings, Diff, Log, Pause func() string
	// Answer carries one choice to whatever the loop is waiting for. The panel decides which
	// actions a boundary offers; the loop decides what each one means.
	Answer func(action, text string) string
	// Scopes lists what the scope picker offers, current state included, and SetScope applies one.
	// A scope decided automatically is still a setting, so choosing here is choosing for good —
	// picking auto again is how the decision is handed back.
	Scopes   func() []ScopeOption
	SetScope func(spec string) string
	Finish   func() (string, bool)
}

// panelInput is an open text field: what it is asking for, what has been typed, and what to do
// with it. The panel has two things to type — an instruction for the author and a scope — and they
// differ only in those three values.
type panelInput struct {
	label  string
	value  string
	submit func(string) string
}

// panelUI is the panel's own transient state. None of it outlives the pane, and none of it is loop
// state: closing the panel while a list is open cancels nothing but the list.
type panelUI struct {
	message string
	input   *panelInput
	picking bool
	picked  int
	exit    bool
}

// Panel keeps no mutable loop state: each refresh gets its display state from files
// and callbacks, so closing the pane cannot cancel the detached loop process.
//
// wake redraws the panel as soon as something happened rather than on the next tick. It is a
// signal, never data: what changed is read back from the files that own it.
func Panel(in, out *os.File, refresh func() PanelState, wake <-chan struct{}, actions PanelActions) error {
	if !IsTTY(in) || !IsTTY(out) {
		_, _ = fmt.Fprint(out, PanelView(refresh(), 80, 24))
		return nil
	}
	terminal := &Terminal{In: in, Out: out}
	if err := terminal.Open(); err != nil {
		return err
	}
	defer terminal.Close()
	keys := make(chan string, 1)
	go func() {
		reader := NewKeyReader(in)
		for {
			key, err := reader.ReadKey()
			if err != nil {
				return
			}
			keys <- key
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	// finish closes the panel by terminating this process. Handling the signal
	// rather than dying under it lets the terminal be restored on the way out.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM)
	defer signal.Stop(quit)
	pane := panelUI{}
	for {
		state := refresh()
		if pane.input != nil {
			state.Message = pane.input.label + ": " + pane.input.value + Invert(" ")
		}
		if pane.message != "" {
			state.Message = pane.message
		}
		if pane.picking && actions.Scopes != nil {
			state.Picker, state.Picked = actions.Scopes(), pane.picked
		}
		width, rows := terminal.Size()
		terminal.Frame(PanelView(state, width, rows))
		select {
		case key := <-keys:
			pane = panelKey(key, state, actions, pane)
			if pane.exit {
				return nil
			}
		case <-quit:
			return nil
		case <-wake:
		case <-tick.C:
		case <-resize:
		}
	}
}

// panelKey folds one keypress into the panel's state. An open input or an open list takes every
// key: a pane with two ways to read the same letter is a pane that surprises people.
func panelKey(key string, state PanelState, actions PanelActions, pane panelUI) panelUI {
	if pane.input != nil {
		return inputKey(key, pane)
	}
	if pane.picking {
		return pickerKey(key, state, actions, pane)
	}
	if state.Pending != "" {
		if answered, handled := pendingKey(key, state, actions, pane); handled {
			return answered
		}
	}
	switch shortcut(key) {
	case "q", "\x03", "esc":
		pane.exit = true
	case "r":
		if state.Pending == "" && !state.Busy {
			pane.message = actions.Review()
		}
	case "o":
		if state.Pending == "" && !state.Busy {
			pane.message = actions.OneShot()
		}
	case "e":
		if actions.Scopes != nil {
			pane.message, pane.picking, pane.picked = "", true, currentScope(actions.Scopes())
		}
	case "p":
		if state.Running && state.Pending == "" {
			pane.message = actions.Pause()
		}
	case "v":
		pane.message = actions.Findings()
	case "d":
		pane.message = actions.Diff()
	case "l":
		pane.message = actions.Log()
	case "x":
		pane.message = actions.Stop()
	case "s":
		pane.message = actions.Settings()
	case "h":
		pane.message = actions.History()
	case "f":
		pane.message, pane.exit = actions.Finish()
	}
	return pane
}

// pendingKey answers the boundary the loop is waiting at, and reports whether this key was one of
// that boundary's own. Everything else falls through to the keys the panel always has.
func pendingKey(key string, state PanelState, actions PanelActions, pane panelUI) (panelUI, bool) {
	switch shortcut(key) {
	case "a":
		if state.Pending == PendingOneShot {
			pane.message = actions.Answer("apply", "")
			return pane, true
		}
	case "c":
		if state.Pending == PendingOneShot {
			pane.message = actions.Answer("cancel", "")
			return pane, true
		}
		if state.Pending == PendingGate {
			pane.message = actions.Answer("continue", "")
			return pane, true
		}
	case "n":
		if state.Pending == PendingGate {
			pane.message = actions.Answer("step", "")
			return pane, true
		}
	case "i":
		pane.message, pane.input = "", &panelInput{label: "instruction", submit: func(text string) string {
			return actions.Answer("instruct", text)
		}}
		return pane, true
	}
	return pane, false
}

// currentScope is the row the picker opens on: what a run started now would use.
func currentScope(options []ScopeOption) int {
	for index, option := range options {
		if option.Current {
			return index
		}
	}
	return 0
}

// pickerKey drives the open list. Choosing an option with no spec opens the input instead, which
// is how the scopes that take a ref or a path are reached.
func pickerKey(key string, state PanelState, actions PanelActions, pane panelUI) panelUI {
	switch shortcut(key) {
	case "esc", "q", "\x03":
		pane.picking = false
	case "j", "down":
		pane.picked = min(pane.picked+1, len(state.Picker)-1)
	case "k", "up":
		pane.picked = max(pane.picked-1, 0)
	case "\r", "\n", "enter":
		if pane.picked >= len(state.Picker) {
			return pane
		}
		option := state.Picker[pane.picked]
		pane.picking = false
		if option.Spec == "" {
			pane.input = &panelInput{label: "scope", submit: actions.SetScope}
			return pane
		}
		pane.message = actions.SetScope(option.Spec)
	}
	return pane
}

// inputKey drives an open text field. The raw key is used rather than the shortcut, because this
// is the one place a keypress means the character it produced.
func inputKey(key string, pane panelUI) panelUI {
	switch key {
	case "esc":
		pane.message, pane.input = pane.input.label+" canceled", nil
	case "\r", "\n":
		if pane.input.value != "" {
			pane.message, pane.input = pane.input.submit(pane.input.value), nil
		}
	case "\x7f", "\b":
		runes := []rune(pane.input.value)
		if len(runes) > 0 {
			pane.input.value = string(runes[:len(runes)-1])
		}
	default:
		if printable(key) {
			pane.input.value += key
		}
	}
	return pane
}
