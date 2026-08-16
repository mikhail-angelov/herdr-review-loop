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

func TestCallReturnsCanceledContext(t *testing.T) {
	client := Client{Binary: command(t, `sleep 5`)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Call(ctx, "agent", "list")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}

func TestVoidCommandsAcceptEmptySuccessfulOutput(t *testing.T) {
	client := Client{Binary: command(t, `exit 0`)}
	ctx := context.Background()
	commands := []struct {
		name string
		run  func() error
	}{
		{"agent send-keys", func() error { return client.AgentSendKeys(ctx, "p", "enter") }},
		{"pane send-text", func() error { return client.PaneSendText(ctx, "p", "/clear") }},
		{"pane send-keys", func() error { return client.PaneSendKeys(ctx, "p", "enter") }},
		{"workspace report-metadata", func() error { return client.WorkspaceReportMetadata(ctx, "w", "review 1/10", false) }},
	}
	for _, test := range commands {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("error envelope", func(t *testing.T) {
		client := Client{Binary: command(t, `printf '%s\n' '{"error":{"code":"rejected","message":"rejected command"}}'`)}
		err := client.PaneSendText(ctx, "p", "/clear")
		var coded *Error
		if !errors.As(err, &coded) || coded.Code != "rejected" {
			t.Fatalf("got %T %v", err, err)
		}
	})
	t.Run("error envelope on stdout with nonzero exit", func(t *testing.T) {
		client := Client{Binary: command(t, `printf '%s\n' '{"error":{"code":"pane_not_found","message":"no such pane"}}'; exit 1`)}
		err := client.PaneSendText(ctx, "p", "/clear")
		var coded *Error
		if !errors.As(err, &coded) || coded.Code != "pane_not_found" || coded.Message != "no such pane" {
			t.Fatalf("got %T %v", err, err)
		}
	})
}

func TestPaneReadReturnsJSONLookingTextVerbatim(t *testing.T) {
	client := Client{Binary: command(t, `printf '%s\n' '{"error":{"code":"agent_prompt_stalled","message":"stalled"}}'`)}
	got, err := client.PaneRead(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	const want = "{\"error\":{\"code\":\"agent_prompt_stalled\",\"message\":\"stalled\"}}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
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
