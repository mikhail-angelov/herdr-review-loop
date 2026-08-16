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
	Running, OneShotPending          bool
}

// PanelView renders one frame of the panel into width columns and rows lines.
func PanelView(state PanelState, width, rows int) string {
	if width < 1 || rows < 1 {
		return ""
	}
	status := "idle"
	if state.Running {
		status = "● running"
	}
	header := []string{Bold(Clip("herdr-review-loop  "+status, width)), Clip("author   "+state.Author, width), Clip("review by "+state.Reviewer, width), Clip(state.Phase, width), strings.Repeat("─", width)}
	footer := append([]string{""}, panelHints(state, width)...)
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

func panelHints(state PanelState, width int) []string {
	hints := []string{"r review", "o one-shot", "f finish", "h history", "s settings", "q close"}
	if state.OneShotPending {
		hints = []string{"a apply", "c cancel", "i instruction", "x stop", "q close"}
	} else if state.Running {
		hints = []string{"x stop", "s settings", "q close"}
	}
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
	Review, OneShot, Stop, Settings, History, Apply, Cancel func() string
	Custom                                                  func(string) string
	Finish                                                  func() (string, bool)
}

// Panel keeps no mutable loop state: each refresh gets its display state from files
// and callbacks, so closing the pane cannot cancel the detached loop process.
func Panel(in, out *os.File, refresh func() PanelState, actions PanelActions) error {
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
			state.Message = "custom instruction: " + input + Invert(" ")
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
		return actions.Review(), false, input, false
	case "o":
		return actions.OneShot(), false, input, false
	case "a":
		if state.OneShotPending {
			return actions.Apply(), false, input, false
		}
	case "c":
		if state.OneShotPending {
			return actions.Cancel(), false, input, false
		}
	case "i":
		if state.OneShotPending {
			return "", true, "", false
		}
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
		return "custom instruction canceled", false, "", false
	case "\r", "\n":
		if input != "" {
			return actions.Custom(input), false, "", false
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
