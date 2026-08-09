package loop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

type fakeAgentClient struct {
	get      func() (herdr.Agent, error)
	wait     func() (herdr.Agent, error)
	prompt   func() (herdr.Agent, error)
	keys     int
	visible  []string
	sentText []string
	sentKeys []string
	focused  []string
}

func (f *fakeAgentClient) AgentGet(context.Context, string) (herdr.Agent, error) { return f.get() }
func (f *fakeAgentClient) AgentWait(context.Context, string, time.Duration) (herdr.Agent, error) {
	return f.wait()
}
func (f *fakeAgentClient) AgentPrompt(context.Context, string, string, time.Duration) (herdr.Agent, error) {
	return f.prompt()
}
func (f *fakeAgentClient) AgentFocus(_ context.Context, target string) error {
	f.focused = append(f.focused, target)
	return nil
}
func (f *fakeAgentClient) AgentSendKeys(context.Context, string, string) error {
	f.keys++
	return nil
}
func (f *fakeAgentClient) PaneSendText(_ context.Context, _ string, value string) error {
	f.sentText = append(f.sentText, value)
	return nil
}
func (f *fakeAgentClient) PaneSendKeys(_ context.Context, _ string, value string) error {
	f.sentKeys = append(f.sentKeys, value)
	return nil
}
func (f *fakeAgentClient) PaneRead(context.Context, string) (string, error) {
	if len(f.visible) == 0 {
		return "", nil
	}
	value := f.visible[0]
	f.visible = f.visible[1:]
	return value, nil
}

func TestSettleWaitsForWorkingAgent(t *testing.T) {
	fake := &fakeAgentClient{
		get:  func() (herdr.Agent, error) { return herdr.Agent{PaneID: "p", Status: "working"}, nil },
		wait: func() (herdr.Agent, error) { return herdr.Agent{PaneID: "p", Status: "idle"}, nil },
	}
	got, err := Settle(context.Background(), fake, herdr.Agent{PaneID: "p"})
	if err != nil || got.Status != "idle" {
		t.Fatalf("got %#v %v", got, err)
	}
}

func TestSubmitAndWaitRetriesStalledPrompt(t *testing.T) {
	getCalls := 0
	fake := &fakeAgentClient{
		get: func() (herdr.Agent, error) {
			getCalls++
			return herdr.Agent{PaneID: "p", StateChangeSeq: int64(getCalls - 1)}, nil
		},
		prompt: func() (herdr.Agent, error) {
			return herdr.Agent{}, &herdr.Error{Code: "agent_prompt_stalled", Message: "stalled"}
		},
		wait: func() (herdr.Agent, error) { return herdr.Agent{PaneID: "p", Status: "idle"}, nil },
	}
	got, err := SubmitAndWait(context.Background(), fake, herdr.Agent{PaneID: "p"}, "prompt")
	if err != nil || got.Status != "idle" || fake.keys != 0 {
		t.Fatalf("got %#v %v, enter presses %d", got, err, fake.keys)
	}
}

func TestResetSessionRetriesDroppedCommand(t *testing.T) {
	fake := &fakeAgentClient{
		visible: []string{"nothing here", "\n/new\n"},
		get:     func() (herdr.Agent, error) { return herdr.Agent{}, nil },
		wait:    func() (herdr.Agent, error) { return herdr.Agent{}, nil },
		prompt:  func() (herdr.Agent, error) { return herdr.Agent{}, errors.New("unused") },
	}
	if err := ResetSession(context.Background(), fake, herdr.Agent{PaneID: "p", Kind: "codex"}, "", nil); err != nil {
		t.Fatal(err)
	}
	if len(fake.sentText) != 2 || len(fake.sentKeys) != 2 || fake.sentKeys[0] != "esc" || fake.sentKeys[1] != "enter" {
		t.Fatalf("unexpected reset interaction: %#v %#v", fake.sentText, fake.sentKeys)
	}
}

func TestResetSessionFailsWhenCommandIsDropped(t *testing.T) {
	fake := &fakeAgentClient{
		visible: []string{"nothing", "nothing", "nothing"},
		get:     func() (herdr.Agent, error) { return herdr.Agent{}, nil },
		wait:    func() (herdr.Agent, error) { return herdr.Agent{}, nil },
		prompt:  func() (herdr.Agent, error) { return herdr.Agent{}, errors.New("unused") },
	}
	if err := ResetSession(context.Background(), fake, herdr.Agent{PaneID: "p", Kind: "codex"}, "", nil); err == nil {
		t.Fatal("accepted a session that was not reset")
	}
}
