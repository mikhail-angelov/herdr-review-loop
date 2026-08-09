package ui

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

func DumpSettings(out *os.File, directory string, values config.Values) {
	_, _ = fmt.Fprintln(out, config.Path(directory))
	for _, field := range config.Fields() {
		_, _ = fmt.Fprintf(out, "%s: %s\n", field.Label, config.Show(field.Key, values))
	}
}

func Settings(in, out *os.File, directory string, values config.Values, status func() string, stop func() string) error {
	if !IsTTY(in) || !IsTTY(out) {
		DumpSettings(out, directory, values)
		return nil
	}
	terminal := &Terminal{In: in, Out: out}
	if err := terminal.Open(); err != nil {
		return err
	}
	defer terminal.Close()
	fields := config.Fields()
	original := values
	dirty := false
	confirmQuit := false
	selected := 0
	message := "j/k move · enter edit · d default · s save · x cancel run · q close"
	editing := false
	input := ""
	keys := make(chan keyResult, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		reader := NewKeyReader(bufio.NewReader(in), in)
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
		width, _ := terminal.Size()
		currentStatus := ""
		if status != nil {
			currentStatus = status()
		}
		terminal.Frame(settingsView(directory, values, selected, message, currentStatus, width, editing, input))
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
		if editing {
			switch key {
			case "esc":
				editing = false
				message = "edit cancelled"
			case "\r", "\n":
				parsed, err := config.Parse(fields[selected].Key, input)
				if err != nil {
					message = err.Error()
					continue
				}
				_ = config.Apply(&values, fields[selected].Key, parsed)
				editing = false
				message = "updated"
				dirty = values != original
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
			continue
		}
		switch key {
		case "j", "down":
			if selected < len(fields)-1 {
				selected++
			}
		case "k", "up":
			if selected > 0 {
				selected--
			}
		case "d":
			defaults := config.Defaults()
			raw := config.Show(fields[selected].Key, defaults)
			parsed, err := config.Parse(fields[selected].Key, raw)
			if err == nil {
				_ = config.Apply(&values, fields[selected].Key, parsed)
				message = "restored default"
				dirty = values != original
			}
		case "s":
			path, err := config.Save(directory, values)
			if err != nil {
				message = err.Error()
			} else {
				message = "saved " + path
				original = values
				dirty = false
			}
		case "x":
			if stop == nil {
				message = "cancellation is unavailable"
			} else {
				message = stop()
			}
		case "q", "\x03", "esc":
			if dirty && !confirmQuit {
				confirmQuit = true
				message = "unsaved changes — press q again to discard"
				continue
			}
			return nil
		case "\r", "\n":
			editing = true
			input = config.Show(fields[selected].Key, values)
			message = "enter accept · esc cancel"
		}
	}
}

type keyResult struct {
	key string
	err error
}

func settingsView(directory string, values config.Values, selected int, message, status string, width int, editing bool, input string) string {
	header := config.Path(directory)
	if status != "" {
		header = status
	}
	var body strings.Builder
	fmt.Fprintf(&body, "%s\n%s\n\n", Bold("herdr-review-loop settings"), Clip(header, width))
	defaults := config.Defaults()
	fields := config.Fields()
	for index, field := range fields {
		marker := " "
		if index == selected {
			marker = "›"
		}
		shown := Clip(config.Show(field.Key, values), width-22)
		if config.Show(field.Key, values) == config.Show(field.Key, defaults) {
			shown = Dim(shown)
		}
		fmt.Fprintf(&body, "%s %-18s %s\n", marker, field.Label, shown)
	}
	if editing {
		fmt.Fprintf(&body, "\n%s: %s", Bold(fields[selected].Label), Clip(input, width-4))
	}
	fmt.Fprintf(&body, "\n%s", Clip(message, width))
	return body.String()
}
