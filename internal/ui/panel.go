package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
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
	header := []string{Clip("\x1b[1mherdr-review-loop  "+status+"\x1b[0m", width), Clip("author   "+state.Author, width), Clip("review by "+state.Reviewer, width), Clip(state.Phase, width), strings.Repeat("─", width)}
	footer := append([]string{""}, strings.Split(Lines("r review · x stop · s settings · q close", width), "\n")...)
	tail := []string{}
	for _, line := range strings.Split(state.Tail, "\n") {
		if width < 44 && strings.HasPrefix(line, "[") {
			if end := strings.Index(line, "] "); end >= 0 {
				line = line[end+2:]
			}
		}
		tail = append(tail, strings.Split(Lines(line, width), "\n")...)
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
	return strings.Join(lines, "\n")
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
	keys := make(chan byte, 1)
	go func() {
		reader := bufio.NewReader(in)
		for {
			key, err := reader.ReadByte()
			if err != nil {
				return
			}
			keys <- key
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
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
			switch key {
			case 'q', 3:
				return nil
			case 'r':
				message = review()
			case 'x':
				message = stop()
			case 's':
				message = settings()
			}
		case <-tick.C:
		}
	}
}
