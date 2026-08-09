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

func Settings(in, out *os.File, directory string, values config.Values) error {
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
	selected := 0
	message := "j/k move · enter edit · d default · s save · q close"
	reader := bufio.NewReader(in)
	for {
		width, _ := terminal.Size()
		var body strings.Builder
		fmt.Fprintf(&body, "\x1b[1mherdr-review-loop settings\x1b[0m\n%s\n\n", Clip(config.Path(directory), width))
		for index, field := range fields {
			marker := " "
			if index == selected {
				marker = "›"
			}
			fmt.Fprintf(&body, "%s %-18s %s\n", marker, field.Label, Clip(config.Show(field.Key, values), width-22))
		}
		fmt.Fprintf(&body, "\n%s", Clip(message, width))
		terminal.Frame(body.String())
		key, err := reader.ReadByte()
		if err != nil {
			return err
		}
		switch key {
		case 'j', 'B':
			if selected < len(fields)-1 {
				selected++
			}
		case 'k', 'A':
			if selected > 0 {
				selected--
			}
		case 'd':
			defaults := config.Defaults()
			raw := config.Show(fields[selected].Key, defaults)
			parsed, err := config.Parse(fields[selected].Key, raw)
			if err == nil {
				_ = config.Apply(&values, fields[selected].Key, parsed)
				message = "restored default"
			}
		case 's':
			path, err := config.Save(directory, values)
			if err != nil {
				message = err.Error()
			} else {
				message = "saved " + path
			}
		case 'q', 3:
			return nil
		case '\r', '\n':
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
			}
		}
	}
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
