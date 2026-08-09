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

func PanelView(state PanelState, width int) string {
	status := "idle"
	if state.Running {
		status = "● running"
	}
	if width < 1 {
		return ""
	}
	lines := []string{"\x1b[1mherdr-review-loop  " + status + "\x1b[0m", "author   " + state.Author, "review by " + state.Reviewer, state.Phase, strings.Repeat("─", width)}
	for _, line := range strings.Split(Lines(state.Tail, width), "\n") {
		lines = append(lines, Clip(line, width))
	}
	lines = append(lines, "")
	lines = append(lines, strings.Split(Lines("r review · x stop · s settings · q close", width), "\n")...)
	if state.Message != "" {
		lines = append(lines, Clip(state.Message, width))
	}
	return strings.Join(lines, "\n")
}

// Panel keeps no mutable loop state: each refresh gets its display state from files
// and callbacks, so closing the pane cannot cancel the detached loop process.
func Panel(in, out *os.File, refresh func() PanelState, review, stop, settings func() string) error {
	if !IsTTY(in) || !IsTTY(out) {
		_, _ = fmt.Fprint(out, PanelView(refresh(), 80))
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
		state.Message = message
		terminal.Frame(PanelView(state, terminal.Size()))
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
