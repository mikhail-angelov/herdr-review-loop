package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

var errEditCancelled = errors.New("edit cancelled")

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
	reader := bufio.NewReader(in)
	for {
		width, _ := terminal.Size()
		currentStatus := ""
		if status != nil {
			currentStatus = status()
		}
		terminal.Frame(settingsView(directory, values, selected, message, currentStatus, width))
		key, err := ReadKey(reader, in)
		if err != nil {
			return err
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
			terminal.Frame("\x1b[1m" + fields[selected].Label + "\x1b[0m: ")
			line, err := readRawLine(reader, out)
			if err != nil {
				if errors.Is(err, errEditCancelled) {
					message = "edit cancelled"
					continue
				}
				return err
			}
			parsed, err := config.Parse(fields[selected].Key, strings.TrimSpace(line))
			if err != nil {
				message = err.Error()
			} else {
				_ = config.Apply(&values, fields[selected].Key, parsed)
				message = "updated"
				dirty = values != original
			}
		}
	}
}

func settingsView(directory string, values config.Values, selected int, message, status string, width int) string {
	header := config.Path(directory)
	if status != "" {
		header = status
	}
	var body strings.Builder
	fmt.Fprintf(&body, "\x1b[1mherdr-review-loop settings\x1b[0m\n%s\n\n", Clip(header, width))
	defaults := config.Defaults()
	for index, field := range config.Fields() {
		marker := " "
		if index == selected {
			marker = "›"
		}
		shown := Clip(config.Show(field.Key, values), width-22)
		if config.Show(field.Key, values) == config.Show(field.Key, defaults) {
			shown = "\x1b[2m" + shown + "\x1b[0m"
		}
		fmt.Fprintf(&body, "%s %-18s %s\n", marker, field.Label, shown)
	}
	fmt.Fprintf(&body, "\n%s", Clip(message, width))
	return body.String()
}

func readRawLine(reader *bufio.Reader, out *os.File) (string, error) {
	var line strings.Builder
	for {
		key, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		switch key {
		case 27:
			return "", errEditCancelled
		case '\r', '\n':
			_, _ = fmt.Fprint(out, "\r\n")
			return line.String(), nil
		case 127, 8:
			runes := []rune(line.String())
			if len(runes) > 0 {
				line.Reset()
				line.WriteString(string(runes[:len(runes)-1]))
				_, _ = fmt.Fprint(out, "\b \b")
			}
		default:
			if key >= 32 && key != 127 {
				line.WriteByte(key)
				_, _ = fmt.Fprintf(out, "%c", key)
			}
		}
	}
}
