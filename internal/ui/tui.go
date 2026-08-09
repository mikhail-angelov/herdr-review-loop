// Package ui contains the small terminal interfaces used by the plugin panes.
package ui

import (
	"bufio"
	"errors"
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

type KeyReader struct {
	bytes   chan byteResult
	pending []string
}

type byteResult struct {
	value byte
	err   error
}

var errKeyTimeout = errors.New("key read timeout")

func NewKeyReader(reader *bufio.Reader, input *os.File) *KeyReader {
	bytes := make(chan byteResult, 1)
	go func() {
		for {
			value, err := reader.ReadByte()
			bytes <- byteResult{value: value, err: err}
			if err != nil {
				return
			}
		}
	}()
	_ = input
	return &KeyReader{bytes: bytes}
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

// ReadKey keeps terminal escape sequences atomic. An unfinished sequence is
// delivered as its literal bytes after 40 ms, so a malformed or interrupted
// sequence can neither freeze a pane nor turn into a close command.
func (r *KeyReader) ReadKey() (string, error) {
	if len(r.pending) > 0 {
		key := r.pending[0]
		r.pending = r.pending[1:]
		return key, nil
	}
	first, err := r.readByte()
	if err != nil {
		return "", err
	}
	if first != 27 {
		return string(first), nil
	}
	sequence := []byte{first}
	second, err := r.readByteAfter(40 * time.Millisecond)
	if err != nil {
		if errors.Is(err, errKeyTimeout) {
			return "esc", nil
		}
		return "", err
	}
	sequence = append(sequence, second)
	if second != '[' && second != 'O' {
		r.enqueueLiteral(sequence[1:])
		return "esc", nil
	}
	for {
		next, readErr := r.readByteAfter(40 * time.Millisecond)
		if readErr != nil {
			if errors.Is(readErr, errKeyTimeout) || errors.Is(readErr, io.EOF) {
				r.enqueueLiteral(sequence)
				return r.ReadKey()
			}
			return "", readErr
		}
		sequence = append(sequence, next)
		if next >= '@' && next <= '~' {
			break
		}
	}
	value := string(sequence[1:])
	if value == "[A" || value == "OA" {
		return "up", nil
	}
	if value == "[B" || value == "OB" {
		return "down", nil
	}
	if value == "[200~" || value == "[201~" {
		return "paste", nil
	}
	return string(sequence), nil
}

func (r *KeyReader) readByteAfter(timeout time.Duration) (byte, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-r.bytes:
		return result.value, result.err
	case <-timer.C:
		return 0, errKeyTimeout
	}
}

func (r *KeyReader) readByte() (byte, error) {
	result := <-r.bytes
	return result.value, result.err
}

func (r *KeyReader) enqueueLiteral(bytes []byte) {
	for _, value := range bytes {
		r.pending = append(r.pending, string(value))
	}
}
