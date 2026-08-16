package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// HistoryRound is one review round as the history pane lists it.
type HistoryRound struct {
	Label   string
	HasDiff bool
}

// HistoryRun is one recorded run and the rounds it produced.
type HistoryRun struct {
	Title  string
	Rounds []HistoryRound
}

// HistoryActions builds the commands the pane hands to the terminal. Diffs and
// findings are documents, so they are handed to the user's own pager instead of
// being rendered here: that buys scrolling, search and highlighting for no code,
// and keeps this pane the list it is good at being.
//
// Restore returns a command as text and never runs it. Rolling a working tree
// back is the one destructive thing history can offer, it cannot be made exact
// without deleting files created after the checkpoint, and the user is better
// placed than this pane to decide whether that is what they want.
type HistoryActions struct {
	Findings  func(run, round int) *exec.Cmd
	RoundDiff func(run, round int) *exec.Cmd
	RunDiff   func(run int) *exec.Cmd
	Restore   func(run, round int) string
}

// HistoryView clips before styling, never after: Clip measures runes and an
// escape sequence would otherwise be counted as width and cut in half.
func HistoryView(runs []HistoryRun, run, round int, inRun bool, message string, width, rows int) string {
	if width < 1 || rows < 1 {
		return ""
	}
	lines := []string{"", Bold(Clip("  herdr-review-loop · history", width))}
	if len(runs) == 0 {
		lines = append(lines, "", Clip("  no recorded runs yet", width), "", Dim(Clip("  q close", width)))
		return frame(lines, rows)
	}
	reserved := 2 // blank line and key hints
	if message != "" {
		reserved += 2
	}
	first, last := historyWindow(len(runs), run, rows-len(lines)-reserved)
	for index := first; index < last; index++ {
		item := runs[index]
		marker := "   "
		if index == run {
			marker = " > "
		}
		line := Clip(marker+item.Title, width)
		if index != run {
			line = Dim(line)
		}
		lines = append(lines, line)
		if index != run || !inRun {
			continue
		}
		for position, entry := range item.Rounds {
			pointer := "     "
			if position == round {
				pointer = "   > "
			}
			suffix := "  —"
			if entry.HasDiff {
				suffix = "  diff"
			}
			line := Clip(pointer+entry.Label+suffix, width)
			if position != round {
				line = Dim(line)
			}
			lines = append(lines, line)
		}
		if len(item.Rounds) == 0 {
			lines = append(lines, Dim(Clip("     no archived rounds", width)))
		}
	}
	hint := "j/k move · enter open · q close"
	if inRun {
		hint = "enter findings · d round diff · a run diff · c restore · esc back · q close"
	}
	lines = append(lines, "", Dim(Clip("  "+hint, width)))
	if message != "" {
		lines = append(lines, "", Clip("  "+message, width))
	}
	return frame(lines, rows)
}

// historyWindow returns a page of runs containing selected. The history state
// is retained in full, but a small popup only renders the slice it can show.
func historyWindow(length, selected, rows int) (first, last int) {
	if length == 0 || rows <= 0 {
		return 0, 0
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= length {
		selected = length - 1
	}
	count := min(length, rows)
	first = max(selected-count/2, 0)
	if first+count > length {
		first = length - count
	}
	return first, first + count
}

func frame(lines []string, rows int) string {
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

func hasSelectedRound(runs []HistoryRun, run, round int, inRun bool) bool {
	return inRun && run >= 0 && run < len(runs) && round >= 0 && round < len(runs[run].Rounds)
}

func performRoundAction(runs []HistoryRun, run, round int, inRun bool, action func()) bool {
	if !hasSelectedRound(runs, run, round, inRun) {
		return false
	}
	action()
	return true
}

// DumpHistory prints the run list as plain text, for when the pane is not attached to a terminal.
func DumpHistory(out *os.File, runs []HistoryRun) {
	if len(runs) == 0 {
		_, _ = fmt.Fprintln(out, "no recorded runs yet")
		return
	}
	for _, run := range runs {
		_, _ = fmt.Fprintln(out, run.Title)
		for _, round := range run.Rounds {
			suffix := ""
			if round.HasDiff {
				suffix = "  diff"
			}
			_, _ = fmt.Fprintln(out, "  "+round.Label+suffix)
		}
	}
}

// History runs the interactive run browser until the user closes it.
func History(in, out *os.File, runs []HistoryRun, actions HistoryActions) error {
	if !IsTTY(in) || !IsTTY(out) {
		DumpHistory(out, runs)
		return nil
	}
	terminal := &Terminal{In: in, Out: out}
	if err := terminal.Open(); err != nil {
		return err
	}
	defer terminal.Close()
	// Read keys in this goroutine. A pager temporarily owns the terminal while
	// it runs, so a background reader would steal its input and replay it here.
	reader := NewKeyReader(in)
	run, round, inRun, message := 0, 0, false, ""
	page := func(command *exec.Cmd) {
		if command == nil {
			message = "nothing to show for this round"
			return
		}
		message = ""
		if err := terminal.Suspend(func() error {
			// a command that already has stdin is being fed rendered text, not a file to open
			if command.Stdin == nil {
				command.Stdin = in
			}
			command.Stdout, command.Stderr = out, os.Stderr
			return command.Run()
		}); err != nil {
			message = err.Error()
		}
	}
	for {
		width, rows := terminal.Size()
		terminal.Frame(HistoryView(runs, run, round, inRun, message, width, rows))
		key, err := reader.ReadKey()
		if err != nil {
			return nil
		}
		switch shortcut(key) {
		case "q", "\x03":
			return nil
		case "esc":
			if !inRun {
				return nil
			}
			inRun, message = false, ""
		case "down", "j":
			if inRun {
				round = next(round, len(runs[run].Rounds))
			} else {
				run = next(run, len(runs))
			}
		case "up", "k":
			if inRun {
				round = previous(round, len(runs[run].Rounds))
			} else {
				run = previous(run, len(runs))
			}
		case "enter", "\r", "\n":
			if len(runs) == 0 {
				continue
			}
			if !inRun {
				inRun, round, message = true, 0, ""
				continue
			}
			if !performRoundAction(runs, run, round, inRun, func() { page(actions.Findings(run, round)) }) {
				message = "no archived rounds"
			}
		case "d":
			if inRun && !performRoundAction(runs, run, round, inRun, func() { page(actions.RoundDiff(run, round)) }) {
				message = "no archived rounds"
			}
		case "a":
			if inRun {
				page(actions.RunDiff(run))
			}
		case "c":
			if inRun && !performRoundAction(runs, run, round, inRun, func() { message = actions.Restore(run, round) }) {
				message = "no archived rounds"
			}
		}
	}
}

func next(current, length int) int {
	if length == 0 || current+1 >= length {
		return current
	}
	return current + 1
}

func previous(current, length int) int {
	if length == 0 || current == 0 {
		return current
	}
	return current - 1
}
