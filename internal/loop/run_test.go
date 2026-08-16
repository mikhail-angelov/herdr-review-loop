package loop

import (
	"context"
	"encoding/json"
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

// runClient is a pair of agents that answer by writing the file the prompt names. The prompt is
// parsed for that path, which is also how the test asserts the loop hands out absolute paths.
type runClient struct {
	agents    []herdr.Agent
	reviews   []string
	decisions []string
	prompted  []string
	prompts   []string
	sentText  []string
	onFocus   func(string) error
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

func (c *runClient) AgentPrompt(_ context.Context, target, prompt string, _ time.Duration) (herdr.Agent, error) {
	c.prompted = append(c.prompted, target)
	c.prompts = append(c.prompts, prompt)
	answers := &c.reviews
	name := reviewFile
	if target == "author" {
		answers, name = &c.decisions, decisionsFile
	}
	if len(*answers) > 0 {
		answer := (*answers)[0]
		*answers = (*answers)[1:]
		if answer != "" {
			if err := os.WriteFile(promptedPath(prompt, name), []byte(answer), 0o600); err != nil {
				return herdr.Agent{}, err
			}
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
func (*runClient) PaneSendKeys(context.Context, string, string) error { return nil }
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

// promptedPath finds the absolute path the prompt told the agent to write.
func promptedPath(prompt, name string) string {
	for field := range strings.FieldsSeq(strings.NewReplacer(",", " ", "\n", " ").Replace(prompt)) {
		if strings.HasSuffix(field, "/"+name) && filepath.IsAbs(field) {
			return field
		}
	}
	return ""
}

func newRun(t *testing.T, client *runClient, values config.Values) (run Run, repository, state string) {
	t.Helper()
	repository, state = t.TempDir(), t.TempDir()
	return Run{
		Client: client,
		Config: values,
		Environment: herdr.Environment{
			Context:  herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository},
			StateDir: state,
		},
		Log:        Log{StateDir: state},
		OutputWait: 50 * time.Millisecond,
	}, repository, state
}

func pairedAgents() []herdr.Agent {
	return []herdr.Agent{
		{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
		{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "claude", Status: "idle"},
	}
}

func quickValues() config.Values {
	values := config.Defaults()
	values.MaxIterations = 1
	values.Retries = 0
	return values
}

func latestReport(t *testing.T, state string) Report {
	t.Helper()
	runs := ListRuns(state)
	if len(runs) == 0 {
		t.Fatal("no run was recorded")
	}
	report, found := ReadReport(state, runs[0].ID)
	if !found {
		t.Fatal("no report was written")
	}
	return report
}

func TestRunEndsCleanAndLeavesOnlyCodeInTheWorkingTree(t *testing.T) {
	client := &runClient{agents: pairedAgents(), reviews: []string{`{"status":"clean","findings":[]}`}}
	run, repository, state := newRun(t, client, quickValues())
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got, want := client.prompted, []string{"reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want %#v", got, want)
	}
	for _, name := range []string{"review.md", "review-summary.md"} {
		if _, err := os.Lstat(filepath.Join(repository, name)); !os.IsNotExist(err) {
			t.Fatalf("%s is in the working tree: %v", name, err)
		}
	}
	report := latestReport(t, state)
	if report.ExitCode != ExitClean || !strings.Contains(report.Outcome, "clean") {
		t.Fatalf("report %+v", report)
	}
}

func TestRunAppliesFindingsThenConverges(t *testing.T) {
	client := &runClient{
		agents: pairedAgents(),
		reviews: []string{
			`{"status":"findings","findings":[
			   {"file":"a.go","line":3,"category":"correctness","severity":"high","title":"leaks a handle","body":"why","fix":"close it"},
			   {"file":"b.go","category":"style","severity":"low","title":"stale comment"}]}`,
			`{"status":"clean","findings":[]}`,
		},
		decisions: []string{`{"tests":{"ran":true,"outcome":"go test ./... passed"},"decisions":[
		    {"id":"r01-1","action":"applied","note":"closed it"},
		    {"id":"r01-2","action":"rejected","note":"the comment is current"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	run, repository, state := newRun(t, client, values)
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got, want := client.prompted, []string{"reviewer", "author", "reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want %#v", got, want)
	}
	// the author's file must carry the ids it was asked to decide on
	authored, err := os.ReadFile(filepath.Join(repository, RunSubdir, "round-01", reviewFile))
	if err != nil {
		t.Fatal(err)
	}
	var handed Review
	if err := json.Unmarshal(authored, &handed); err != nil {
		t.Fatal(err)
	}
	if len(handed.Findings) != 2 || handed.Findings[0].ID != "r01-1" || handed.Findings[0].Fingerprint == "" {
		t.Fatalf("the author was handed %s", authored)
	}
	// the second round's prompt must carry the refusal forward
	if !strings.Contains(client.prompts[2], "the comment is current") {
		t.Fatalf("round 2 did not carry the decision journal:\n%s", client.prompts[2])
	}
	report := latestReport(t, state)
	if len(report.Findings) != 2 {
		t.Fatalf("report findings %#v", report.Findings)
	}
	actions := map[string]string{}
	for _, finding := range report.Findings {
		actions[finding.ID] = finding.Action
	}
	if actions["r01-1"] != ActionApplied || actions["r01-2"] != ActionRejected {
		t.Fatalf("actions %#v", actions)
	}
}

func TestRunRecordsWhatTheAuthorPhaseChanged(t *testing.T) {
	repository := newRepository(t)
	state := t.TempDir()
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{`{"status":"findings","findings":[{"file":"tracked.txt","title":"needs a test"}]}`, `{"status":"clean","findings":[]}`},
		decisions: []string{`{"tests":{"ran":true,"outcome":"passed"},
		  "decisions":[{"id":"r01-1","action":"applied","note":"added one"}]}`},
	}
	// the author edits a tracked file and creates a new one, which is what a fix usually looks like
	client.onFocus = func(target string) error {
		if target != "author" {
			return nil
		}
		if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("changed\n"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(repository, "new_test.go"), []byte("package main\n"), 0o600)
	}
	values := quickValues()
	values.MaxIterations = 2
	run := Run{
		Client:      client,
		Config:      values,
		Environment: herdr.Environment{Context: herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository}, StateDir: state},
		Log:         Log{StateDir: state},
		OutputWait:  50 * time.Millisecond,
	}
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile(filepath.Join(ArchiveDir(state, ListRuns(state)[0].ID), "round-01", changesPatch))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tracked.txt", "new_test.go", "+changed"} {
		if !strings.Contains(string(patch), want) {
			t.Fatalf("the round patch is missing %q:\n%s", want, patch)
		}
	}
	if strings.Contains(string(patch), PluginDir) {
		t.Fatalf("the round patch contains the loop's own files:\n%s", patch)
	}
}

func TestRunAcceptsAMarkdownReviewWithoutLosingARound(t *testing.T) {
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{"STATUS: FINDINGS\n- [high] a.go:1 — broken — fix it\n"},
	}
	run, _, state := newRun(t, client, quickValues())
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitFindings {
		t.Fatalf("got %v (code %d), want the budget to be spent", err, ExitCode(err))
	}
	if got, want := client.prompted, []string{"reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v: a markdown review should not cost an extra turn", got)
	}
	report := latestReport(t, state)
	if len(report.Findings) != 1 || report.Findings[0].Action != ActionUnreviewed {
		t.Fatalf("report %#v", report.Findings)
	}
}

func TestRunSpendsOneReformatTurnOnGarbledOutput(t *testing.T) {
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{"I could not finish, sorry.", `{"status":"clean","findings":[]}`},
	}
	run, _, state := newRun(t, client, quickValues())
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got, want := client.prompted, []string{"reviewer", "reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want one review and one reformat", got)
	}
	if !strings.Contains(client.prompts[1], "Do not review anything again") {
		t.Fatalf("the second turn was not a reformat: %s", client.prompts[1])
	}
	events := ReadEvents(ArchiveDir(state, ListRuns(state)[0].ID))
	if !slices.ContainsFunc(events, func(e Event) bool { return e.Event == EventParseFallback }) {
		t.Fatalf("the fallback was not recorded: %#v", events)
	}
}

func TestRunNeverExitsCleanOnAContradictoryReview(t *testing.T) {
	// status findings with an empty array is a truncated turn, not clean code
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{`{"status":"findings","findings":[]}`, `{"status":"findings","findings":[]}`},
	}
	values := quickValues()
	values.Retries = 0
	run, _, state := newRun(t, client, values)
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitAgent {
		t.Fatalf("got %v (code %d), want an agent failure", err, ExitCode(err))
	}
	if report := latestReport(t, state); report.ExitCode != ExitAgent {
		t.Fatalf("report %+v", report)
	}
}

func TestRunStopsForAHumanOnAnOpenQuestion(t *testing.T) {
	client := &runClient{agents: pairedAgents(), reviews: []string{
		`{"status":"clean","findings":[{"file":"a.go","title":"also found this"}],
		  "open_questions":[{"question":"is the 5m budget meant to cover a reformat turn?"}]}`,
	}}
	run, _, state := newRun(t, client, quickValues())
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitBlocked {
		t.Fatalf("got %v (code %d), want a human to be needed", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "5m budget") {
		t.Fatalf("the question was not named: %v", err)
	}
	if got := client.prompted; len(got) != 1 {
		t.Fatalf("prompted %#v: an author phase followed a question", got)
	}
	report := latestReport(t, state)
	if len(report.OpenQuestions) != 1 {
		t.Fatalf("questions %#v", report.OpenQuestions)
	}
	if len(report.Findings) != 1 || report.Findings[0].Action != ActionUnreviewed {
		t.Fatalf("the findings raised alongside the question were lost: %#v", report.Findings)
	}
}

func TestRunStopsWhenTheAuthorDecidesNothing(t *testing.T) {
	client := &runClient{
		agents:    pairedAgents(),
		reviews:   []string{`{"status":"findings","findings":[{"file":"a.go","title":"fix me"}]}`},
		decisions: []string{`{"decisions":[]}`, `{"decisions":[]}`},
	}
	values := quickValues()
	values.MaxIterations, values.Retries = 3, 0
	run, _, _ := newRun(t, client, values)
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitAgent {
		t.Fatalf("got %v (code %d), want an agent failure", err, ExitCode(err))
	}
	if got, want := client.prompted, []string{"reviewer", "author", "author"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want %#v", got, want)
	}
}

func TestRunRetriesAReviewPhaseAndThenGivesUp(t *testing.T) {
	client := &runClient{agents: pairedAgents(), reviews: []string{"garbage", "still garbage", "garbage again", "and again"}}
	values := quickValues()
	values.Retries = 1
	run, _, state := newRun(t, client, values)
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitAgent {
		t.Fatalf("got %v (code %d), want an agent failure", err, ExitCode(err))
	}
	// two attempts, each costing a review turn and a reformat turn
	if len(client.prompted) != 4 {
		t.Fatalf("prompted %#v", client.prompted)
	}
	events := ReadEvents(ArchiveDir(state, ListRuns(state)[0].ID))
	if !slices.ContainsFunc(events, func(e Event) bool { return e.Event == EventRetry }) {
		t.Fatalf("no retry was recorded: %#v", events)
	}
}

func TestRunRefusesARepositoryThatTracksTheRunDirectory(t *testing.T) {
	repository := newRepository(t)
	if err := os.MkdirAll(filepath.Join(repository, RunSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, repository, RunSubdir+"/leftover.json", "{}")
	if _, err := gitOutput(context.Background(), repository, "add", "-f", RunSubdir); err != nil {
		t.Fatal(err)
	}
	client := &runClient{agents: pairedAgents()}
	run := Run{
		Client:      client,
		Config:      quickValues(),
		Environment: herdr.Environment{Context: herdr.Context{FocusedPaneID: "author", WorkspaceCWD: repository}, StateDir: t.TempDir()},
		OutputWait:  50 * time.Millisecond,
	}
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitTool {
		t.Fatalf("got %v (code %d), want a tool error", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "git rm -r --cached") || !strings.Contains(err.Error(), "leftover.json") {
		t.Fatalf("the error does not name the fix or the file: %v", err)
	}
	if len(client.prompted) != 0 {
		t.Fatalf("the loop reviewed anyway: %#v", client.prompted)
	}
}

func TestDryRunTakesNoLockAndDescribesTheRun(t *testing.T) {
	client := &runClient{agents: []herdr.Agent{
		{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
		{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "other", Status: "idle"},
	}}
	run, repository, state := newRun(t, client, config.Defaults())
	if err := run.Execute(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lockPath(state)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created a lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository, PluginDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created a run directory: %v", err)
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
			client := &runClient{agents: []herdr.Agent{
				{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: test.authorKind, Status: "idle"},
				{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: test.reviewerKind, Status: "idle"},
			}}
			run, _, state := newRun(t, client, config.Defaults())
			err := run.Execute(context.Background(), false)
			if err == nil || !strings.Contains(err.Error(), "no reset command") {
				t.Fatalf("got %v, want a missing-reset error", err)
			}
			if ExitCode(err) != ExitTool {
				t.Fatalf("code %d, want a tool error", ExitCode(err))
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

func TestRunStopsWhenTheReviewerIsBlocked(t *testing.T) {
	agents := pairedAgents()
	agents[1].Status = "blocked"
	client := &runClient{agents: agents}
	run, _, _ := newRun(t, client, quickValues())
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitBlocked {
		t.Fatalf("got %v (code %d), want a human to be needed", err, ExitCode(err))
	}
}
