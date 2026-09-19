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
	Events                           []string
	Running                          bool
	// Pending names the boundary the loop is waiting at, empty when it is not waiting. Paused is
	// the request that produces one: asked for at any moment, honored at the next boundary.
	Pending string
	Paused  bool
	Summary string
	// Busy is a loop running in another space. One runs at a time, so this panel can show its own
	// history and nothing else: offering to start a review that the lock would refuse is worse
	// than not offering one.
	Busy bool
}

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
	status := "idle"
	if state.Running {
		status = "● running"
	}
	if state.Pending != "" {
		status = "⏸ waiting for you"
	} else if state.Paused && state.Running {
		status = "⏸ pausing"
	}
	header := []string{Bold(Clip("herdr-review-loop  "+status, width)), Clip("author   "+state.Author, width), Clip("review by "+state.Reviewer, width), Clip(state.Phase, width)}
	if state.Summary != "" {
		header = append(header, Clip(state.Summary, width))
	}
	header = append(header, strings.Repeat("─", width))
	// the hints are laid out against the rows left once the header is drawn, because a panel that
	// scrolled its own keys off the bottom would be a panel nobody can drive
	footer := append([]string{""}, panelHints(state, width, rows-len(header)-1)...)
	tail := make([]string, 0, len(state.Events))
	for _, line := range state.Events {
		if width < 44 && strings.HasPrefix(line, "[") {
			if end := strings.Index(line, "] "); end >= 0 {
				line = line[end+2:]
			}
		}
		tail = append(tail, Clip(line, width))
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

// panelHints are the keys that do something in the state the panel is in. A key that would be
// refused is not offered: the panel is the only place these boundaries are explained.
func panelHints(state PanelState, width, rows int) []string {
	hints := []string{"r review", "o one-shot", "f finish", "v findings", "d diff", "h history", "s settings", "q close"}
	switch {
	case state.Busy && !state.Running:
		hints = []string{"v findings", "d diff", "h history", "s settings", "q close"}
	case state.Pending == PendingOneShot:
		hints = []string{"a apply", "c cancel", "i instruction", "v findings", "d diff", "x stop", "q close"}
	case state.Pending == PendingGate:
		hints = []string{"c continue", "n one round", "i instruction", "v findings", "d diff", "x stop", "q close"}
	case state.Running && state.Paused:
		hints = []string{"p resume", "x stop", "v findings", "d diff", "q close"}
	case state.Running:
		hints = []string{"p pause", "x stop", "v findings", "d diff", "s settings", "q close"}
	}
	lines := wrapHints(hints, width)
	// a short pane keeps the first key and the one that closes the pane, and gives up the ones in
	// between: they are all reachable from Herdr's own action menu, and those two are not
	for len(lines) > rows && len(hints) > 2 {
		hints = append(hints[:len(hints)-2], hints[len(hints)-1])
		lines = wrapHints(hints, width)
	}
	return lines
}

func wrapHints(hints []string, width int) []string {
	lines := []string{""}
	for _, hint := range hints {
		last := len(lines) - 1
		joined := hint
		if lines[last] != "" {
			joined = lines[last] + " · " + hint
		}
		if len([]rune(joined)) <= width {
			lines[last] = joined
		} else {
			lines = append(lines, hint)
		}
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
	Review, OneShot, Stop, Settings, History, Findings, Diff, Pause func() string
	// Answer carries one choice to whatever the loop is waiting for. The panel decides which
	// actions a boundary offers; the loop decides what each one means.
	Answer func(action, text string) string
	Finish func() (string, bool)
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
	message := ""
	custom, input := false, ""
	for {
		state := refresh()
		if custom {
			state.Message = "instruction: " + input + Invert(" ")
		}
		if message != "" {
			state.Message = message
		}
		width, rows := terminal.Size()
		terminal.Frame(PanelView(state, width, rows))
		select {
		case key := <-keys:
			var exitPanel bool
			message, custom, input, exitPanel = panelKey(key, state, actions, message, custom, input)
			if exitPanel {
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

func panelKey(key string, state PanelState, actions PanelActions, message string, custom bool, input string) (nextMessage string, nextCustom bool, nextInput string, exitPanel bool) {
	if custom {
		return customPanelKey(key, actions, message, input)
	}
	switch shortcut(key) {
	case "q", "\x03", "esc":
		return message, false, input, true
	case "r":
		if state.Pending == "" && !state.Busy {
			return actions.Review(), false, input, false
		}
	case "o":
		if state.Pending == "" && !state.Busy {
			return actions.OneShot(), false, input, false
		}
	case "a":
		if state.Pending == PendingOneShot {
			return actions.Answer("apply", ""), false, input, false
		}
	case "c":
		if state.Pending == PendingOneShot {
			return actions.Answer("cancel", ""), false, input, false
		}
		if state.Pending == PendingGate {
			return actions.Answer("continue", ""), false, input, false
		}
	case "n":
		if state.Pending == PendingGate {
			return actions.Answer("step", ""), false, input, false
		}
	case "i":
		if state.Pending != "" {
			return "", true, "", false
		}
	case "p":
		if state.Running && state.Pending == "" {
			return actions.Pause(), false, input, false
		}
	case "v":
		return actions.Findings(), false, input, false
	case "d":
		return actions.Diff(), false, input, false
	case "x":
		return actions.Stop(), false, input, false
	case "s":
		return actions.Settings(), false, input, false
	case "h":
		return actions.History(), false, input, false
	case "f":
		result, finished := actions.Finish()
		return result, false, input, finished
	}
	return message, false, input, false
}

func customPanelKey(key string, actions PanelActions, message, input string) (nextMessage string, nextCustom bool, nextInput string, exitPanel bool) {
	switch key {
	case "esc":
		return "instruction canceled", false, "", false
	case "\r", "\n":
		if input != "" {
			return actions.Answer("instruct", input), false, "", false
		}
	case "\x7f", "\b":
		runes := []rune(input)
		if len(runes) > 0 {
			input = string(runes[:len(runes)-1])
		}
	default:
		if len(key) == 1 && key[0] >= 32 {
			input += key
		}
	}
	return message, true, input, false
}
