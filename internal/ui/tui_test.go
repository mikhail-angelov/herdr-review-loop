package ui

import (
	"bufio"
	"os"
	"testing"
	"time"
)

func TestKeyReaderRecognizesTerminalSequences(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()
	defer func() { _ = write.Close() }()
	if _, err := write.Write([]byte("\x1b[")); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = write.Write([]byte("A\x1bOB\x1b[200~"))
	}()
	keys := NewKeyReader(bufio.NewReader(read), read)
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
	if _, err := write.Write([]byte("\x1b[")); err != nil {
		t.Fatal(err)
	}
	keys := NewKeyReader(bufio.NewReader(read), read)
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
	if _, err := write.Write([]byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	keys := NewKeyReader(bufio.NewReader(read), read)
	if got, err := keys.ReadKey(); err != nil || got != "esc" {
		t.Fatalf("got %q %v, want lone escape", got, err)
	}
}
