// Package ui contains the small terminal interfaces used by the plugin panes.
package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// IsTTY reports whether the file is a terminal, which is how a pane tells interactive use from a
// redirected dump.
func IsTTY(file *os.File) bool { return term.IsTerminal(int(file.Fd())) }

// Terminal is a pane's raw-mode terminal. Open must be paired with Close.
type Terminal struct {
	In    *os.File
	Out   io.Writer
	state *term.State
}

// KeyReader turns a terminal's byte stream into whole key presses, escape sequences included.
type KeyReader struct {
	input   *os.File
	bytes   []byte
	pending []string
}

var errKeyTimeout = errors.New("key read timeout")

// shortcut keeps physical TUI shortcuts usable with a Russian keyboard layout.
func shortcut(key string) string {
	switch key {
	case "й", "Й":
		return "q"
	case "ы", "Ы":
		return "s"
	case "к", "К":
		return "r"
	case "ч", "Ч":
		return "x"
	case "в", "В":
		return "d"
	case "ф", "Ф":
		return "a"
	case "с", "С":
		return "c"
	case "а", "А":
		return "f"
	case "о", "О":
		return "j"
	case "л", "Л":
		return "k"
	default:
		return key
	}
}

// NewKeyReader reads keys from the given terminal input.
func NewKeyReader(input *os.File) *KeyReader { return &KeyReader{input: input} }

// Open puts the terminal in raw mode and hides the cursor.
func (t *Terminal) Open() error {
	state, err := term.MakeRaw(int(t.In.Fd()))
	if err != nil {
		return fmt.Errorf("failed to enter raw mode: %w", err)
	}
	t.state = state
	_, _ = fmt.Fprint(t.Out, "\x1b[?25l")
	return nil
}

// Close restores the terminal to the state Open found it in.
func (t *Terminal) Close() {
	if t.state != nil {
		_ = term.Restore(int(t.In.Fd()), t.state)
	}
	_, _ = fmt.Fprint(t.Out, "\x1b[?25h\x1b[0m\n")
}

// Suspend hands the terminal back to a child process — a pager, or git driving
// its own — and takes it again afterwards. Raw mode and the hidden cursor are
// this pane's, not the child's, so both are undone before it starts.
func (t *Terminal) Suspend(run func() error) error {
	t.Close()
	err := run()
	if openErr := t.Open(); err == nil {
		err = openErr
	}
	return err
}

// Size is the terminal's width and height, falling back to 80x24 when it cannot be measured.
func (t *Terminal) Size() (width, height int) {
	file, ok := t.Out.(*os.File)
	if !ok {
		return 80, 24
	}
	width, height, err := term.GetSize(int(file.Fd()))
	if err != nil || width < 1 {
		return 80, 24
	}
	return width, height //nolint:nakedret // named only to document the pair
}

// Frame clears the screen and draws one complete frame.
func (t *Terminal) Frame(contents string) {
	// MakeRaw disables output post-processing, so a bare LF moves down without
	// returning to column zero. Frames are assembled with LF; write CRLF so each
	// rendered line starts at the left edge instead of forming a staircase.
	_, _ = fmt.Fprintf(t.Out, "\x1b[2J\x1b[H%s", strings.ReplaceAll(contents, "\n", "\r\n"))
}

// Clip shortens a value to width, marking the cut with an ellipsis.
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

// Lines hard-wraps every line to width, so a frame's height is predictable.
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
		if first >= utf8.RuneSelf {
			sequence := []byte{first}
			for !utf8.FullRune(sequence) {
				next, readErr := r.readByte()
				if readErr != nil {
					return "", readErr
				}
				sequence = append(sequence, next)
			}
			return string(sequence), nil
		}
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
	if len(r.bytes) > 0 {
		return r.readByte()
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, errKeyTimeout
		}
		milliseconds := int((remaining + time.Millisecond - 1) / time.Millisecond)
		ready, err := unix.Poll([]unix.PollFd{{Fd: descriptor(r.input), Events: unix.POLLIN}}, milliseconds)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("failed to poll terminal input: %w", err)
		}
		if ready == 0 {
			return 0, errKeyTimeout
		}
		return r.readByte()
	}
}

func (r *KeyReader) readByte() (byte, error) {
	if len(r.bytes) > 0 {
		value := r.bytes[0]
		r.bytes = r.bytes[1:]
		return value, nil
	}
	var buffer [64]byte
	read, err := r.input.Read(buffer[:])
	if read > 1 {
		r.bytes = append(r.bytes, buffer[1:read]...)
	}
	if read > 0 {
		return buffer[0], nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to read terminal input: %w", err)
	}
	return 0, io.EOF
}

// descriptor narrows a file descriptor for poll. Descriptors are small non-negative integers, so
// the conversion cannot lose anything.
func descriptor(file *os.File) int32 { return int32(file.Fd()) } //nolint:gosec // fds fit in int32

func (r *KeyReader) enqueueLiteral(bytes []byte) {
	for _, value := range bytes {
		r.pending = append(r.pending, string(value))
	}
}
