package ui

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestFrameUsesCarriageReturnInRawMode(t *testing.T) {
	var output bytes.Buffer
	terminal := Terminal{Out: &output}
	terminal.Frame("first\nsecond")
	if got, want := output.String(), "\x1b[2J\x1b[Hfirst\r\nsecond"; got != want {
		t.Fatalf("frame = %q, want %q", got, want)
	}
}

func TestKeyReaderRecognizesTerminalSequences(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	if _, err := write.WriteString("\x1b["); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = write.WriteString("A\x1bOB\x1b[200~")
	}()
	keys := NewKeyReader(read)
	for _, want := range []string{"up", "down", "paste"} {
		got, err := keys.ReadKey()
		if err != nil || got != want {
			t.Fatalf("got %q %v, want %q", got, err, want)
		}
	}
}

func TestKeyReaderDeliversUnfinishedSequenceLiterally(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	if _, err := write.WriteString("\x1b["); err != nil {
		t.Fatal(err)
	}
	keys := NewKeyReader(read)
	for _, want := range []string{"\x1b", "["} {
		got, err := keys.ReadKey()
		if err != nil || got != want {
			t.Fatalf("got %q %v, want %q", got, err, want)
		}
	}
}

func TestKeyReaderDeliversLoneEscapeAfterTimeout(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	if _, err := write.WriteString("\x1b"); err != nil {
		t.Fatal(err)
	}
	keys := NewKeyReader(read)
	if got, err := keys.ReadKey(); err != nil || got != "esc" {
		t.Fatalf("got %q %v, want lone escape", got, err)
	}
}

func TestKeyReaderKeepsUTF8KeysWhole(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	if _, err := write.WriteString("йыК"); err != nil {
		t.Fatal(err)
	}
	keys := NewKeyReader(read)
	for _, want := range []string{"й", "ы", "К"} {
		got, err := keys.ReadKey()
		if err != nil || got != want {
			t.Fatalf("got %q %v, want %q", got, err, want)
		}
	}
}

func TestShortcutSupportsRussianKeyboardLayout(t *testing.T) {
	for key, want := range map[string]string{"й": "q", "ы": "s", "к": "r", "ч": "x", "в": "d", "ф": "a", "с": "c", "а": "f", "о": "j", "л": "k"} {
		if got := shortcut(key); got != want {
			t.Errorf("shortcut(%q) = %q, want %q", key, got, want)
		}
	}
}
