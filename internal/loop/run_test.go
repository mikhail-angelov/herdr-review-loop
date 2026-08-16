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
	// only the turn that names the file is the one an agent answers by writing it; a native review
	// command's own turn renders into the host UI and writes nothing, which is why there is a
	// capture step at all
	if promptedPath(prompt, name) == "" {
		return c.AgentGet(context.Background(), target)
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

func newRun(t *testing.T, client Client, values config.Values) (run Run, repository, state string) {
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

// pairedAgents reviews with a kind that has no native review command, so these tests exercise the
// built-in prompt — one turn, one file. The delegated path each supported kind takes instead has
// its own tests in adapter_test.go and TestANativeReviewerRunsItsOwnCommandThenRecordsIt.
func pairedAgents() []herdr.Agent {
	return []herdr.Agent{
		{PaneID: "author", WorkspaceID: "workspace", Name: "author", Kind: "codex", Status: "idle"},
		{PaneID: "reviewer", WorkspaceID: "workspace", Name: "reviewer", Kind: "gemini", Status: "idle"},
	}
}

// resolvedValues is what a run with no configuration at all gets, which is what these tests should
// exercise: config.Defaults alone is the floor beneath the default profile, never a real run.
func resolvedValues() config.Values { return config.Resolve(config.Sources{}).Values }

func quickValues() config.Values {
	values := resolvedValues()
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
	// a round the loop only understood in the older form has to be legible as one in the archive
	events := ReadEvents(ArchiveDir(state, ListRuns(state)[0].ID))
	if !slices.ContainsFunc(events, func(e Event) bool {
		return e.Event == EventParseFallback && strings.Contains(e.Detail, "markdown")
	}) {
		t.Fatalf("the markdown fallback was not recorded: %#v", events)
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
	round := filepath.Join(ArchiveDir(state, ListRuns(state)[0].ID), "round-01")
	for name, want := range map[string]string{
		rawReview:                      "I could not finish, sorry.",
		"prompt-review-reformat-01.md": client.prompts[1],
		"review-reformat-01.raw.txt":   `{"status":"clean","findings":[]}`,
	} {
		data, err := os.ReadFile(filepath.Join(round, name))
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q %v, want %q", name, data, err, want)
		}
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

func TestOneShotReviewWaitsForThePanelThenRunsOneAuthorPhase(t *testing.T) {
	client := &runClient{
		agents:    pairedAgents(),
		reviews:   []string{`{"status":"findings","findings":[{"file":"a.go","title":"fix me"}]}`},
		decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"done"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 10
	run, repository, state := newRun(t, client, values)
	run.OneShot = true
	done := make(chan error, 1)
	go func() { done <- run.Execute(context.Background(), false) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, pending := PendingOneShot(state, "workspace", repository); pending {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("one-shot review ended before waiting for the panel: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("one-shot review never waited for the panel")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := ChooseOneShot(state, "workspace", repository, OneShotApply, ""); err != nil {
		t.Fatal(err)
	}
	if err := <-done; ExitCode(err) != ExitFindings {
		t.Fatalf("got %v (code %d), want one-shot completion with unverified findings", err, ExitCode(err))
	}
	if got, want := client.prompted, []string{"reviewer", "author", "author"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want %#v", got, want)
	}
	if _, pending := PendingOneShot(state, "workspace", repository); pending {
		t.Fatal("one-shot choice was not cleared")
	}
	report := latestReport(t, state)
	if len(report.Findings) != 1 || report.Findings[0].Action != ActionApplied {
		t.Fatalf("report %#v", report.Findings)
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
	run, repository, state := newRun(t, client, resolvedValues())
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

// TestTheRoundPolicyDrivesTheReviewAndTheAuthor is T7.3's own criterion: a project profile with
// fewer entries than the round budget has to keep applying from its last entry onward.
func TestTheRoundPolicyDrivesTheReviewAndTheAuthor(t *testing.T) {
	client := &runClient{
		agents: pairedAgents(),
		reviews: []string{
			`{"status":"findings","findings":[{"file":"a.go","line":1,"category":"correctness","severity":"high","title":"leaks a handle"}]}`,
			`{"status":"findings","findings":[{"file":"a.go","line":2,"category":"correctness","severity":"high","title":"still leaks"}]}`,
			`{"status":"clean","findings":[]}`,
		},
		decisions: []string{
			`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`,
			`{"decisions":[{"id":"r02-1","action":"applied","note":"closed it properly"}]}`,
		},
	}
	values := quickValues()
	values.MaxIterations = 3
	values.Rounds = []config.Round{{Level: "high"}, {Level: "low", Instructions: "closing out only"}}
	run, _, _ := newRun(t, client, values)
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// prompts are reviewer, author, reviewer, author, reviewer
	if len(client.prompts) != 5 {
		t.Fatalf("prompted %#v", client.prompted)
	}
	if !strings.Contains(client.prompts[0], "broad pass") {
		t.Fatalf("round 1 did not use the policy's first entry:\n%s", client.prompts[0])
	}
	for _, round := range []int{2, 4} {
		if !strings.Contains(client.prompts[round], "closing out only") {
			t.Fatalf("round %d did not repeat the policy's last entry:\n%s", round/2+1, client.prompts[round])
		}
	}
	if !strings.Contains(client.prompts[3], "only high-severity findings") {
		t.Fatalf("the author was not narrowed with the reviewer:\n%s", client.prompts[3])
	}
}

// TestTheVerdictBarWithholdsFindingsWithoutLosingThem is T7.5's criterion: min_verdict decides
// what reaches the author, and never what a finished run can account for.
func TestTheVerdictBarWithholdsFindingsWithoutLosingThem(t *testing.T) {
	review := `{"status":"findings","findings":[
	   {"file":"a.go","line":1,"category":"correctness","severity":"high","verdict":"confirmed","title":"leaks a handle"},
	   {"file":"b.go","line":2,"category":"style","severity":"low","verdict":"plausible","title":"might be stale"}]}`
	for _, test := range []struct {
		name       string
		minVerdict string
		wantSent   []string
		wantAction map[string]string
	}{
		{"confirmed withholds the rest", config.VerdictConfirmed, []string{"r01-1"}, map[string]string{"r01-1": ActionApplied, "r01-2": ActionFiltered}},
		{"plausible passes everything", config.VerdictPlausible, []string{"r01-1", "r01-2"}, map[string]string{"r01-1": ActionApplied, "r01-2": ActionApplied}},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &runClient{
				agents:  pairedAgents(),
				reviews: []string{review, `{"status":"clean","findings":[]}`},
				decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"},
				    {"id":"r01-2","action":"applied","note":"refreshed it"}]}`},
			}
			values := quickValues()
			values.MaxIterations = 2
			values.MinVerdict = test.minVerdict
			run, repository, state := newRun(t, client, values)
			if err := run.Execute(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			handed, err := os.ReadFile(filepath.Join(repository, RunSubdir, "round-01", reviewFile))
			if err != nil {
				t.Fatal(err)
			}
			var sent Review
			if err := json.Unmarshal(handed, &sent); err != nil {
				t.Fatal(err)
			}
			if len(sent.Findings) != len(test.wantSent) {
				t.Fatalf("the author was handed %s, want ids %v", handed, test.wantSent)
			}
			for index, id := range test.wantSent {
				if sent.Findings[index].ID != id {
					t.Fatalf("the author was handed %s, want ids %v", handed, test.wantSent)
				}
			}
			actions := map[string]string{}
			for _, finding := range latestReport(t, state).Findings {
				actions[finding.ID] = finding.Action
			}
			for id, want := range test.wantAction {
				if actions[id] != want {
					t.Fatalf("report recorded %#v, want %s for %s", actions, want, id)
				}
			}
		})
	}
}

// TestARoundWhoseFindingsAreAllFilteredIsCleanAndSaysSo covers §8's other half: filtering can end
// a run with code 0, and "clean" must never be confused with "nothing survived the bar".
func TestARoundWhoseFindingsAreAllFilteredIsCleanAndSaysSo(t *testing.T) {
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{`{"status":"findings","findings":[{"file":"a.go","line":1,"category":"style","verdict":"plausible","title":"might be stale"}]}`},
	}
	values := quickValues()
	values.MinVerdict = config.VerdictConfirmed
	run, _, state := newRun(t, client, values)
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatalf("a fully filtered round should exit clean, got %v", err)
	}
	if got, want := client.prompted, []string{"reviewer"}; !slices.Equal(got, want) {
		t.Fatalf("prompted %#v, want no author phase", got)
	}
	report := latestReport(t, state)
	if report.ExitCode != ExitClean || !strings.Contains(report.Outcome, "1 filtered") {
		t.Fatalf("report %+v, want a clean outcome that names the filtering", report)
	}
	if len(report.Findings) != 1 || report.Findings[0].Action != ActionFiltered || report.Findings[0].Note != VerdictPlausible {
		t.Fatalf("report findings %#v", report.Findings)
	}
	document, found := LoadRound(state, ListRuns(state)[0].ID, 1)
	if !found {
		t.Fatal("the round was not archived")
	}
	rendered := RenderMarkdown(document)
	for _, want := range []string{"nothing cleared the verdict bar", "## filtered", "might be stale"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("show is missing %q:\n%s", want, rendered)
		}
	}
}

// TestAStuckPairStopsInsteadOfSpendingTheBudget is T8.3's criterion: a reviewer that never accepts
// a fix stops the run in round three rather than in round ten.
func TestAStuckPairStopsInsteadOfSpendingTheBudget(t *testing.T) {
	finding := `{"status":"findings","findings":[{"file":"a.go","line":8,"category":"correctness","severity":"high","title":"context is not canceled on the timeout path"}]}`
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{finding, finding, finding, finding, finding},
		decisions: []string{
			`{"decisions":[{"id":"r01-1","action":"applied","note":"canceled it"}]}`,
			`{"decisions":[{"id":"r02-1","action":"applied","note":"canceled it properly"}]}`,
			`{"decisions":[{"id":"r03-1","action":"applied","note":"canceled it again"}]}`,
			`{"decisions":[{"id":"r04-1","action":"applied","note":"and again"}]}`,
		},
	}
	values := quickValues()
	values.MaxIterations = 10
	run, _, state := newRun(t, client, values)
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitFindings || !strings.Contains(err.Error(), "context is not canceled") {
		t.Fatalf("got %v (code %d), want a stuck run that names the finding", err, ExitCode(err))
	}
	report := latestReport(t, state)
	if report.Rounds != 3 {
		t.Fatalf("the run took %d round(s), want it to stop at 3", report.Rounds)
	}
	// the disputed finding keeps its applied decisions, so the disagreement is legible afterwards
	if len(report.Findings) != 3 {
		t.Fatalf("report findings %#v", report.Findings)
	}
	for _, entry := range report.Findings {
		if entry.Action != ActionApplied {
			t.Fatalf("report findings %#v, want every round's decision kept", report.Findings)
		}
	}
}

func TestADisputeThatSkipsARoundStartsOver(t *testing.T) {
	var disputes Disputes
	review := Review{Findings: []Finding{{ID: "r01-1", Fingerprint: "abc", Title: "leaks"}}}
	applied := Decisions{Decisions: []Decision{{ID: "r01-1", Action: ActionApplied}}}
	for _, round := range []int{1, 2, 4, 5} {
		review.Findings[0].ID = applied.Decisions[0].ID
		disputes.Record(round, review, applied)
		if _, stuck := disputes.Stuck(); stuck {
			t.Fatalf("round %d was called stuck across a gap", round)
		}
	}
	disputes.Record(6, review, applied)
	if _, stuck := disputes.Stuck(); !stuck {
		t.Fatal("three consecutive applied rounds were not called stuck")
	}
}

func TestARejectedFindingIsNotADispute(t *testing.T) {
	var disputes Disputes
	review := Review{Findings: []Finding{{ID: "r01-1", Fingerprint: "abc", Title: "leaks"}}}
	for round := 1; round <= 5; round++ {
		disputes.Record(round, review, Decisions{Decisions: []Decision{{ID: "r01-1", Action: ActionRejected}}})
	}
	if _, stuck := disputes.Stuck(); stuck {
		t.Fatal("a finding the author keeps rejecting is an argument the journal carries, not a stuck loop")
	}
}

// TestATextScopeRunsOverADocument is T9.1's criterion: no argument is indistinguishable from the
// worktree run, and --scope text:<path> runs a meaningful loop over one document.
func TestATextScopeRunsOverADocument(t *testing.T) {
	client := &runClient{
		agents: pairedAgents(),
		reviews: []string{
			`{"status":"findings","findings":[{"file":"docs/plan.md","line":12,"category":"gap","severity":"high","title":"§3 contradicts §7 about the run directory"}]}`,
			`{"status":"clean","findings":[]}`,
		},
		decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"§7 rewritten to match §3"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	values.Scope = "text:docs/plan.md"
	run, repository, state := newRun(t, client, values)
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "plan.md"), []byte("# plan\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	document := filepath.Join(repository, "docs", "plan.md")
	if !strings.Contains(client.prompts[0], document) || strings.Contains(client.prompts[0], "git diff") {
		t.Fatalf("the reviewer was not pointed at the document:\n%s", client.prompts[0])
	}
	if !strings.Contains(client.prompts[1], document) {
		t.Fatalf("the author was not pointed at the document:\n%s", client.prompts[1])
	}
	manifest, found := ReadManifest(state, ListRuns(state)[0].ID)
	if !found || manifest.Scope != values.Scope || !strings.Contains(manifest.ScopeCommand, "docs/plan.md") {
		t.Fatalf("manifest %+v", manifest)
	}
}

func TestATextScopeRefusesADocumentThatIsNotThere(t *testing.T) {
	values := quickValues()
	values.Scope = "text:docs/absent.md"
	run, _, _ := newRun(t, &runClient{agents: pairedAgents()}, values)
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitTool || !strings.Contains(err.Error(), "docs/absent.md") {
		t.Fatalf("got %v (code %d), want a tool error naming the document", err, ExitCode(err))
	}
}

// TestANativeReviewerRunsItsOwnCommandThenRecordsIt is T7.2's criterion: each supported kind runs
// the review command it already ships and still produces a conforming review.json, and a kind with
// no command of its own falls back and produces a valid round all the same.
func TestANativeReviewerRunsItsOwnCommandThenRecordsIt(t *testing.T) {
	for _, test := range []struct {
		kind      string
		wantTurns []string
		wantOne   bool
	}{
		{kind: "codex", wantTurns: []string{"/review "}},
		{kind: "claude", wantTurns: []string{"/code-review high"}},
		{kind: "gemini", wantOne: true},
	} {
		t.Run(test.kind, func(t *testing.T) {
			agents := pairedAgents()
			agents[1].Kind = test.kind
			// the pair must be two kinds, and the author's kind never picks the review command
			agents[0].Kind = "opencode"
			client := &runClient{agents: agents, reviews: []string{`{"status":"clean","findings":[]}`}}
			run, _, state := newRun(t, client, quickValues())
			if err := run.Execute(context.Background(), false); err != nil {
				t.Fatal(err)
			}
			sent := strings.Join(client.prompts, "\n@@@\n")
			for _, want := range test.wantTurns {
				if !strings.Contains(sent, want) {
					t.Fatalf("%s never ran its own review command:\n%s", test.kind, sent)
				}
			}
			if test.wantOne && len(client.prompts) != 1 {
				t.Fatalf("a kind with no review command should need one turn, got %d:\n%s", len(client.prompts), sent)
			}
			if !test.wantOne {
				// the native commands render into the host UI, so the findings only become data
				// once the capture step has asked for them
				if len(client.prompts) < 2 {
					t.Fatalf("%s was never asked to record what it found:\n%s", test.kind, sent)
				}
				if !strings.Contains(client.prompts[len(client.prompts)-1], "record the review you just produced") {
					t.Fatalf("the last turn is not the capture step:\n%s", client.prompts[len(client.prompts)-1])
				}
			}
			if report := latestReport(t, state); report.ExitCode != ExitClean {
				t.Fatalf("%s did not produce a valid round: %+v", test.kind, report)
			}
		})
	}
}

func TestTheDelegatedRequestIsArchivedWholeIncludingEveryTurn(t *testing.T) {
	agents := pairedAgents()
	agents[1].Kind = "claude"
	client := &runClient{agents: agents, reviews: []string{`{"status":"clean","findings":[]}`}}
	run, _, state := newRun(t, client, quickValues())
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(ArchiveDir(state, ListRuns(state)[0].ID), "round-01", promptReview))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"git diff", "/code-review high", "record the review you just produced"} {
		if !strings.Contains(string(archived), want) {
			t.Fatalf("the archived request is missing %q:\n%s", want, archived)
		}
	}
}

func TestARegressionsOnlyRoundIsIgnoredUntilItHasABaseline(t *testing.T) {
	client := &runClient{
		agents:    pairedAgents(),
		reviews:   []string{`{"status":"findings","findings":[{"file":"a.go","line":1,"category":"correctness","title":"leaks"}]}`, `{"status":"clean","findings":[]}`},
		decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"closed it"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	values.Rounds = []config.Round{{Level: "high", RegressionsOnly: true}}
	run, _, state := newRun(t, client, values)
	if err := run.Execute(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.prompts[0], "do not raise anything that predates them") {
		t.Fatalf("round 1 asked for regressions it has no baseline for:\n%s", client.prompts[0])
	}
	if !strings.Contains(readLog(t, state), "ignoring regressions_only") {
		t.Fatal("round 1 dropped regressions_only without saying so")
	}
}

func readLog(t *testing.T, state string) string {
	t.Helper()
	data, err := os.ReadFile(Log{StateDir: state}.Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
			run, _, state := newRun(t, client, resolvedValues())
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
