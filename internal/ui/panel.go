package ui

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type PanelState struct {
	Author, Reviewer, Phase, Tail, Message string
	Running                                bool
}

func PanelView(state PanelState, width, rows int) string {
	if width < 1 || rows < 1 {
		return ""
	}
	status := "idle"
	if state.Running {
		status = "● running"
	}
	header := []string{Bold(Clip("herdr-review-loop  "+status, width)), Clip("author   "+state.Author, width), Clip("review by "+state.Reviewer, width), Clip(state.Phase, width), strings.Repeat("─", width)}
	footer := append([]string{""}, panelHints(state.Running, width)...)
	tail := []string{}
	for _, line := range strings.Split(state.Tail, "\n") {
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
	lines := append(header, tail...)
	lines = append(lines, footer...)
	if state.Message != "" {
		lines = append(lines, Clip(state.Message, width))
	}
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

func panelHints(running bool, width int) []string {
	hints := []string{"r review", "s settings", "q close"}
	if running {
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

// Panel keeps no mutable loop state: each refresh gets its display state from files
// and callbacks, so closing the pane cannot cancel the detached loop process.
func Panel(in, out *os.File, refresh func() PanelState, review, stop, settings func() string) error {
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
		reader := NewKeyReader(bufio.NewReader(in), in)
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
	message := ""
	for {
		state := refresh()
		if message != "" {
			state.Message = message
		}
		width, rows := terminal.Size()
		terminal.Frame(PanelView(state, width, rows))
		select {
		case key := <-keys:
			key = shortcut(key)
			switch key {
			case "q", "\x03", "esc":
				return nil
			case "r":
				message = review()
			case "x":
				message = stop()
			case "s":
				message = settings()
			}
		case <-tick.C:
		case <-resize:
		}
	}
}
