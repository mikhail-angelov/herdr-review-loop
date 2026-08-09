package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

type runClient struct {
	agents     []herdr.Agent
	reviewPath string
	prompted   []string
}

func (c *runClient) AgentList(context.Context) ([]herdr.Agent, error) { return c.agents, nil }
func (c *runClient) AgentGet(_ context.Context, target string) (herdr.Agent, error) {
	for _, agent := range c.agents {
		if herdr.Target(agent) == target {
			return agent, nil
		}
	}
	return herdr.Agent{}, errors.New("missing agent")
}
func (c *runClient) AgentWait(_ context.Context, target string, _ time.Duration) (herdr.Agent, error) {
	return c.AgentGet(context.Background(), target)
}
func (c *runClient) AgentPrompt(_ context.Context, target, _ string, _ time.Duration) (herdr.Agent, error) {
	c.prompted = append(c.prompted, target)
	if target == "reviewer" {
		if err := os.WriteFile(c.reviewPath, []byte("STATUS: CLEAN\n"), 0o644); err != nil {
			return herdr.Agent{}, err
		}
	}
	return c.AgentGet(context.Background(), target)
}
func (*runClient) AgentFocus(context.Context, string) error                            { return nil }
func (*runClient) AgentSendKeys(context.Context, string, string) error                 { return nil }
func (*runClient) PaneSendText(context.Context, string, string) error                  { return nil }
func (*runClient) PaneSendKeys(context.Context, string, string) error                  { return nil }
func (*runClient) PaneRead(context.Context, string) (string, error)                    { return "", nil }
func (*runClient) WorkspaceReportMetadata(context.Context, string, string, bool) error { return nil }
func (*runClient) NotificationShow(context.Context, string, string) error              { return nil }
func (*runClient) PluginPaneOpen(context.Context, string, string) (string, error) {
	return "", errors.New("panel unavailable")
}
func (*runClient) PaneLayout(context.Context, string) (herdr.PaneLayout, error) {
	return herdr.PaneLayout{}, nil
}
func (*runClient) PaneResize(context.Context, string, string, float64) error { return nil }

func TestRunCompletesCleanReviewAndArchivesVerdict(t *testing.T) {
	repository, state := t.TempDir(), t.TempDir()
	client := &runClient{
		agents: []herdr.Agent{
			{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
			{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "other", Status: "idle"},
		},
		reviewPath: filepath.Join(repository, "review.md"),
	}
	values := config.Defaults()
	values.MaxIterations = 1
	run := Run{
		Client: client,
		Config: values,
		Environment: herdr.Environment{
			Context:  herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository},
			StateDir: state,
		},
		Log: Log{StateDir: state},
	}
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(client.prompted) != 1 || client.prompted[0] != "reviewer" {
		t.Fatalf("prompted %#v", client.prompted)
	}
	history, err := filepath.Glob(filepath.Join(state, "history", "*", "iteration-01.md"))
	if err != nil || len(history) != 1 {
		t.Fatalf("history %v %v", history, err)
	}
}

func TestDryRunDoesNotTakeLock(t *testing.T) {
	repository, state := t.TempDir(), t.TempDir()
	client := &runClient{agents: []herdr.Agent{
		{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
		{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "other", Status: "idle"},
	}}
	run := Run{Client: client, Config: config.Defaults(), Environment: herdr.Environment{Context: herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository}, StateDir: state}}
	if err := run.Execute(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath(state)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created a lock: %v", err)
	}
}
