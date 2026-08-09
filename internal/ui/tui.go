// Package ui contains the small terminal interfaces used by the plugin panes.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

func IsTTY(file *os.File) bool { return term.IsTerminal(int(file.Fd())) }

type Terminal struct {
	In    *os.File
	Out   io.Writer
	state *term.State
}

func (t *Terminal) Open() error {
	state, err := term.MakeRaw(int(t.In.Fd()))
	if err != nil {
		return err
	}
	t.state = state
	_, _ = fmt.Fprint(t.Out, "\x1b[?25l")
	return nil
}
func (t *Terminal) Close() {
	if t.state != nil {
		_ = term.Restore(int(t.In.Fd()), t.state)
	}
	_, _ = fmt.Fprint(t.Out, "\x1b[?25h\x1b[0m\n")
}
func (t *Terminal) Size() (int, int) {
	file, ok := t.Out.(*os.File)
	if !ok {
		return 80, 24
	}
	width, height, err := term.GetSize(int(file.Fd()))
	if err != nil || width < 1 {
		return 80, 24
	}
	return width, height
}
func (t *Terminal) Frame(contents string) { _, _ = fmt.Fprintf(t.Out, "\x1b[H\x1b[2J%s", contents) }
func Clip(value string, width int) string {
	if width < 1 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
func Lines(value string, width int) string {
	if width < 1 {
		return value
	}
	var result []string
	for _, line := range strings.Split(value, "\n") {
		for len([]rune(line)) > width {
			result = append(result, string([]rune(line)[:width]))
			line = string([]rune(line)[width:])
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// ReadKey keeps terminal escape sequences atomic. A lone Escape remains usable
// after a short timeout, while arrows and bracketed paste markers are never
// interpreted as their individual bytes.
func ReadKey(reader *bufio.Reader, input *os.File) (string, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	if first != 27 {
		return string(first), nil
	}
	_ = input.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	second, err := reader.ReadByte()
	_ = input.SetReadDeadline(time.Time{})
	if err != nil {
		if os.IsTimeout(err) {
			return "esc", nil
		}
		return "", err
	}
	if second != '[' && second != 'O' {
		return "esc", nil
	}
	var sequence strings.Builder
	sequence.WriteByte(second)
	for {
		next, readErr := reader.ReadByte()
		if readErr != nil {
			return "esc", nil
		}
		sequence.WriteByte(next)
		if next >= '@' && next <= '~' {
			break
		}
	}
	value := sequence.String()
	if value == "[A" || value == "OA" {
		return "up", nil
	}
	if value == "[B" || value == "OB" {
		return "down", nil
	}
	if value == "[200~" || value == "[201~" {
		return "paste", nil
	}
	return "esc", nil
}
