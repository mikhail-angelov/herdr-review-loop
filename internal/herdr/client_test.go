package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCallDecodesEnvelopesFromBothStreams(t *testing.T) {
	client := Client{Binary: command(t, `printf '%s\n' ignored; printf '%s\n' '{"error":{"code":"agent_prompt_stalled","message":"stalled"}}' >&2; exit 1`)}
	_, err := client.Call(context.Background(), "agent", "prompt")
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != "agent_prompt_stalled" || coded.Message != "stalled" {
		t.Fatalf("got %T %v", err, err)
	}
}

func TestCallDecodesResult(t *testing.T) {
	client := Client{Binary: command(t, `printf '%s\n' '{"result":{"ok":true}}'`)}
	raw, err := client.Call(context.Background(), "agent", "list")
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("got %q %v", raw, err)
	}
}

func TestCallReturnsCancelledContext(t *testing.T) {
	client := Client{Binary: command(t, `sleep 5`)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Call(ctx, "agent", "list")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}

func command(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "herdr")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
