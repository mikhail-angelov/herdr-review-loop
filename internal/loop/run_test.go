package loop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

type runClient struct {
	agents        []herdr.Agent
	reviewPath    string
	summaryPath   string
	reviewResults []string
	prompted      []string
	sentText      []string
	sentKeys      []string
	onFocus       func(string) error
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
		result := "STATUS: CLEAN\n"
		if len(c.reviewResults) > 0 {
			result = c.reviewResults[0]
			c.reviewResults = c.reviewResults[1:]
		}
		if err := os.WriteFile(c.reviewPath, []byte(result), 0o644); err != nil {
			return herdr.Agent{}, err
		}
	}
	if target == "author" {
		if err := os.WriteFile(c.summaryPath, []byte("# Review summary\n"), 0o644); err != nil {
			return herdr.Agent{}, err
		}
	}
	return c.AgentGet(context.Background(), target)
}
func (c *runClient) AgentFocus(_ context.Context, target string) error {
	if c.onFocus == nil {
		return nil
	}
	return c.onFocus(target)
}
func (*runClient) AgentSendKeys(context.Context, string, string) error { return nil }
func (c *runClient) PaneSendText(_ context.Context, _, value string) error {
	c.sentText = append(c.sentText, value)
	return nil
}
func (c *runClient) PaneSendKeys(_ context.Context, _, value string) error {
	c.sentKeys = append(c.sentKeys, value)
	return nil
}
func (c *runClient) PaneRead(context.Context, string) (string, error) {
	if len(c.sentText) == 0 {
		return "", nil
	}
	return c.sentText[len(c.sentText)-1], nil
}
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
			{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "claude", Status: "idle"},
		},
		reviewPath:  filepath.Join(repository, "review.md"),
		summaryPath: filepath.Join(repository, "review-summary.md"),
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

func TestRunReservesSummaryFile(t *testing.T) {
	repository := t.TempDir()
	values := config.Defaults()
	values.ReviewFile = SummaryFile
	run := Run{
		Client: &runClient{agents: []herdr.Agent{
			{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
			{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "claude", Status: "idle"},
		}},
		Config: values,
		Environment: herdr.Environment{
			Context: herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository},
		},
	}
	if err := run.Execute(context.Background(), false); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("got %v, want reserved-file error", err)
	}
}

func TestRunFailsBeforeReviewWhenAnAgentHasNoResetCommand(t *testing.T) {
	for _, test := range []struct {
		name                     string
		authorKind, reviewerKind string
	}{
		{"author", "other", "claude"},
		{"reviewer", "codex", "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, state := t.TempDir(), t.TempDir()
			client := &runClient{agents: []herdr.Agent{
				{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: test.authorKind, Status: "idle"},
				{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: test.reviewerKind, Status: "idle"},
			}}
			run := Run{
				Client: client,
				Config: config.Defaults(),
				Environment: herdr.Environment{
					Context:  herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository},
					StateDir: state,
				},
			}
			if err := run.Execute(context.Background(), false); err == nil || !strings.Contains(err.Error(), "no reset command") {
				t.Fatalf("got %v, want missing-reset error", err)
			}
			if len(client.prompted) != 0 {
				t.Fatalf("prompted %#v, want no prompts", client.prompted)
			}
			if _, err := os.Stat(lockPath(state)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing-reset run created a lock: %v", err)
			}
		})
	}
}

func TestRunUsesFallbackResetForUnknownAuthorAndAcceptsAnUnchangedSummary(t *testing.T) {
	repository, state := t.TempDir(), t.TempDir()
	client := &runClient{
		agents: []herdr.Agent{
			{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "other", Status: "idle"},
			{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "claude", Status: "idle"},
		},
		reviewPath:    filepath.Join(repository, "review.md"),
		summaryPath:   filepath.Join(repository, "review-summary.md"),
		reviewResults: []string{"STATUS: FINDINGS\n- [high] file.go:1 — broken — fix it\n", "STATUS: CLEAN\n"},
	}
	values := config.Defaults()
	values.MaxIterations = 2
	values.ResetCommand = "/reset"
	if err := os.WriteFile(client.summaryPath, []byte("# Review summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if got, want := client.prompted, []string{"reviewer", "author", "reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want %#v", got, want)
	}
	if got, want := client.sentText, []string{"/clear", "/reset", "/clear"}; !slices.Equal(got, want) {
		t.Fatalf("reset commands %#v, want %#v", got, want)
	}
	if _, err := os.Stat(client.summaryPath); err != nil {
		t.Fatalf("summary missing: %v", err)
	}
}

func TestRunRemovesSummaryFromPriorRun(t *testing.T) {
	repository, state := t.TempDir(), t.TempDir()
	summaryPath := filepath.Join(repository, SummaryFile)
	if err := os.WriteFile(summaryPath, []byte("- Deferred stale decision\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &runClient{
		agents: []herdr.Agent{
			{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
			{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "claude", Status: "idle"},
		},
		reviewPath:  filepath.Join(repository, "review.md"),
		summaryPath: summaryPath,
	}
	run := Run{
		Client: client,
		Config: config.Defaults(),
		Environment: herdr.Environment{
			Context:  herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository},
			StateDir: state,
		},
		Log: Log{StateDir: state},
	}
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(summaryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("summary survived new run: %v", err)
	}
}

func TestRunStopsWhenSummaryBecomesUnreadableBeforeAuthorTurn(t *testing.T) {
	repository, state := t.TempDir(), t.TempDir()
	summaryPath := filepath.Join(repository, SummaryFile)
	if err := os.WriteFile(summaryPath, []byte("# Review summary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client := &runClient{
		agents: []herdr.Agent{
			{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
			{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "claude", Status: "idle"},
		},
		reviewPath:    filepath.Join(repository, "review.md"),
		summaryPath:   summaryPath,
		reviewResults: []string{"STATUS: FINDINGS\n- [high] file.go:1 — broken — fix it\n"},
		onFocus: func(target string) error {
			if target != "author" {
				return nil
			}
			return os.Mkdir(summaryPath, 0o755)
		},
	}
	run := Run{
		Client: client,
		Config: config.Defaults(),
		Environment: herdr.Environment{
			Context:  herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository},
			StateDir: state,
		},
		Log: Log{StateDir: state},
	}
	if err := run.Execute(context.Background(), false); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("got %v, want unreadable-summary error", err)
	}
	if got, want := client.prompted, []string{"reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want %#v", got, want)
	}
}
