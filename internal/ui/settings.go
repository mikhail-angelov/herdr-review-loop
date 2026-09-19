package ui

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

const settingsKeys = "j/k move · enter edit · d default · s save · x cancel run · q close"

// DumpSettings prints the settings as plain text, for when the pane is not attached to a terminal.
func DumpSettings(out *os.File, directory string, values config.Values) {
	_, _ = fmt.Fprintln(out, config.Path(directory))
	for _, field := range config.Fields() {
		_, _ = fmt.Fprintf(out, "%s: %s\n", field.Label, config.Show(field.Key, values))
	}
}

// settingsPane is the editor's state. It is a struct rather than a pile of locals so the two key
// handlers below can be read on their own, without the event loop around them.
type settingsPane struct {
	directory string
	fields    []config.Field
	values    config.Values
	original  config.Values
	stop      func() string
	message   string
	input     string
	selected  int
	// top is the first field the list shows. A pane shorter than the settings it holds scrolls,
	// and the selection has to stay inside the window or the user is navigating a list they
	// cannot see — which is what the footer alone reporting the move looks like.
	top         int
	editing     bool
	dirty       bool
	confirmQuit bool
}

// Settings runs the interactive settings editor until the user closes it. Nothing is written until
// the user saves, so an abandoned session leaves config.json untouched.
func Settings(in, out *os.File, directory string, values config.Values, status, stop func() string) error {
	if !IsTTY(in) || !IsTTY(out) {
		DumpSettings(out, directory, values)
		return nil
	}
	terminal := &Terminal{In: in, Out: out}
	if err := terminal.Open(); err != nil {
		return err
	}
	defer terminal.Close()
	pane := &settingsPane{directory: directory, fields: config.Fields(), values: values, original: values, stop: stop}
	keys := make(chan keyResult, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		reader := NewKeyReader(in)
		for {
			key, err := reader.ReadKey()
			select {
			case keys <- keyResult{key: key, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	for {
		width, rows := terminal.Size()
		currentStatus := ""
		if status != nil {
			currentStatus = status()
		}
		terminal.Frame(pane.view(currentStatus, width, rows))
		var key string
		select {
		case result := <-keys:
			if result.err != nil {
				return result.err
			}
			key = result.key
		case <-resize:
			continue
		}
		if pane.editing {
			pane.edit(key)
			continue
		}
		if pane.command(shortcut(key)) {
			return nil
		}
	}
}

// edit handles a key while a value is being typed.
func (p *settingsPane) edit(key string) {
	switch key {
	case "esc":
		p.editing = false
		p.message = "edit canceled"
	case "\r", "\n":
		parsed, err := config.Parse(p.fields[p.selected].Key, p.input)
		if err != nil {
			p.message = err.Error()
			return
		}
		_ = config.Apply(&p.values, p.fields[p.selected].Key, parsed)
		p.editing = false
		p.message = "updated"
		p.dirty = !config.Same(p.values, p.original)
	case "\x7f", "\b":
		runes := []rune(p.input)
		if len(runes) > 0 {
			p.input = string(runes[:len(runes)-1])
		}
	default:
		if len(key) == 1 && key[0] >= 32 {
			p.input += key
		}
	}
}

// command handles a key while the list has focus, and reports whether the pane should close.
func (p *settingsPane) command(key string) bool {
	switch key {
	case "j", "down":
		if p.selected < len(p.fields)-1 {
			p.selected++
		}
	case "k", "up":
		if p.selected > 0 {
			p.selected--
		}
	case "d":
		p.restoreDefault()
	case "s":
		p.save()
	case "x":
		if p.stop == nil {
			p.message = "cancellation is unavailable"
			return false
		}
		p.message = p.stop()
	case "q", "\x03", "esc":
		if p.dirty && !p.confirmQuit {
			p.confirmQuit = true
			p.message = "unsaved changes — press q again to discard"
			return false
		}
		return true
	case "\r", "\n":
		p.editing = true
		p.input = config.Show(p.fields[p.selected].Key, p.values)
		p.message = ""
	}
	return false
}

func (p *settingsPane) restoreDefault() {
	key := p.fields[p.selected].Key
	parsed, err := config.Parse(key, config.Show(key, config.Defaults()))
	if err != nil {
		return
	}
	_ = config.Apply(&p.values, key, parsed)
	p.message = "restored default"
	p.dirty = !config.Same(p.values, p.original)
}

func (p *settingsPane) save() {
	path, err := config.Save(p.directory, p.values)
	if err != nil {
		p.message = err.Error()
		return
	}
	p.message = "saved " + path
	p.original = p.values
	p.dirty = false
}

type keyResult struct {
	key string
	err error
}

// settingsChrome is how many lines the view spends on everything that is not the field list: the
// leading blank, the title, the blank under it, and the six-line footer.
const settingsChrome = 9

// window keeps the selection inside the visible slice of the list, scrolling by as little as it
// takes. It is called from the view, because how many fields fit is only known once the pane knows
// how tall it is.
func (p *settingsPane) window(rows int) (top, height int) {
	height = max(rows-settingsChrome, 1)
	if height >= len(p.fields) {
		p.top = 0
		return 0, len(p.fields)
	}
	p.top = min(p.top, len(p.fields)-height)
	p.top = max(p.top, 0)
	if p.selected < p.top {
		p.top = p.selected
	}
	if p.selected >= p.top+height {
		p.top = p.selected - height + 1
	}
	return p.top, height
}

// scrollNote says what the window is hiding, in the blank line the list already had under it, so
// saying it costs no row.
func (p *settingsPane) scrollNote(top, height int) string {
	below := len(p.fields) - top - height
	parts := make([]string, 0, 2)
	if top > 0 {
		parts = append(parts, fmt.Sprintf("↑ %d more", top))
	}
	if below > 0 {
		parts = append(parts, fmt.Sprintf("↓ %d more", below))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  " + Dim(strings.Join(parts, " · "))
}

func (p *settingsPane) view(status string, width, rows int) string {
	if width < 1 || rows < 1 {
		return ""
	}
	header := config.Path(p.directory)
	if status != "" {
		header = status
	}
	defaults := config.Defaults()
	labelWidth := 0
	for _, field := range p.fields {
		labelWidth = max(labelWidth, len([]rune(field.Label)))
	}
	valueWidth := max(width-labelWidth-8, 16)
	lines := []string{"", "  " + Bold("herdr-review-loop settings") + "   " + Dim(Clip(header, width-4)), ""}
	top, height := p.window(rows)
	for index, field := range p.fields[top : top+height] {
		index += top
		label := field.Label + strings.Repeat(" ", labelWidth-len([]rune(field.Label)))
		marker := " "
		if index == p.selected {
			marker = Bold("▸")
		} else {
			label = Dim(label)
		}
		shown := Clip(config.Show(field.Key, p.values), valueWidth)
		switch {
		case index == p.selected && p.editing:
			shown = Clip(p.input, valueWidth) + Invert(" ")
		case config.Show(field.Key, p.values) == config.Show(field.Key, defaults):
			if shown == "" {
				shown = "unset"
			}
			shown = Dim(shown)
		}
		lines = append(lines, fmt.Sprintf("  %s %s  %s", marker, label, shown))
	}
	keys := settingsKeys
	if p.editing {
		keys = "enter accept · esc cancel"
	}
	message := p.message
	if message == keys {
		message = ""
	}
	hint := p.fields[p.selected].Hint
	lines = append(lines, p.scrollNote(top, height), "  "+Dim(Clip(hint, width-4)), "", "  "+Dim(Clip(message, width-4)), "", "  "+Keys(Clip(keys, width-4)))
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}
