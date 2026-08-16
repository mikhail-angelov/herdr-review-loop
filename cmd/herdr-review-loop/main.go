package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
	"github.com/mikhail-angelov/herdr-review-loop/internal/loop"
	"github.com/mikhail-angelov/herdr-review-loop/internal/ui"
)

var version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

var errUsage = errors.New("usage error")

const usage = "usage: herdr-review-loop <review [--dry-run]|stop|finish|open-panel|open-settings|open-history|panel|settings|history|version|help>"

// application is everything a command needs after the environment has been loaded, assembled once
// so the command table below stays a table.
type application struct {
	environment herdr.Environment
	values      config.Values
	client      herdr.Client
	warnings    []string
}

// commands is the whole CLI surface. Dispatching through a table keeps each verb's behavior in one
// named function instead of one long switch.
var commands = map[string]func(application, []string) error{
	"review":        reviewCommand,
	"stop":          stopCommand,
	"finish":        finishCommand,
	"settings":      settingsCommand,
	"open-panel":    openPanelCommand,
	"open-settings": openSettingsCommand,
	"open-history":  openHistoryCommand,
	"panel":         panelCommand,
	"history":       historyCommand,
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "version":
		if len(rest) != 0 {
			return usageError()
		}
		fmt.Println(version)
		return nil
	case "help":
		if len(rest) != 0 {
			return usageError()
		}
		fmt.Println(usage)
		return nil
	}
	command, known := commands[verb]
	if !known {
		return usageError()
	}
	environment, err := herdr.LoadEnvironment()
	if err != nil {
		return err
	}
	values, warnings := config.Load(environment.ConfigDir)
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "herdr-review-loop:", warning)
	}
	return command(application{
		environment: environment,
		values:      values,
		client:      herdr.NewClient(environment.Binary),
		warnings:    warnings,
	}, rest)
}

func reviewCommand(app application, args []string) error {
	dryRun := len(args) == 1 && args[0] == "--dry-run"
	if len(args) != 0 && !dryRun {
		return usageError()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := loop.Log{StateDir: app.environment.StateDir, Output: os.Stdout}
	if !dryRun {
		for _, warning := range app.warnings {
			_ = log.Write(warning)
		}
	}
	err := loop.Run{Client: app.client, Config: app.values, Environment: app.environment, Log: log}.Execute(ctx, dryRun)
	if err != nil && !dryRun {
		_ = log.Write(err.Error())
	}
	return err
}

func stopCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return stopRun(app.client, app.environment)
}

func finishCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	lines, err := finishReview(app.environment, app.values)
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	loop.ClosePanel(app.environment.StateDir, app.environment.Context.WorkspaceID)
	return nil
}

func settingsCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return ui.Settings(os.Stdin, os.Stdout, app.environment.ConfigDir, app.values, func() string {
		return settingsStatus(app.environment.StateDir)
	}, func() string {
		if err := stopRun(app.client, app.environment); err != nil {
			return err.Error()
		}
		return "review loop canceled"
	})
}

// openPanelCommand focuses the workspace's existing panel rather than opening a second one: two
// panels would both claim the same state and fight over it.
func openPanelCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	if existing, live := loop.LivePanel(app.environment.StateDir, app.environment.Context.WorkspaceID); live {
		return app.client.PluginPaneFocus(context.Background(), existing.PaneID)
	}
	return openPane(context.Background(), app.client, "panel", app.environment.Context.FocusedPaneID, false)
}

func openSettingsCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return openPane(context.Background(), app.client, "settings", app.environment.Context.FocusedPaneID, true)
}

func openHistoryCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return openPane(context.Background(), app.client, "history", app.environment.Context.FocusedPaneID, true)
}

func panelCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return panel(app.environment, app.values, app.client)
}

func historyCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return history(app.environment)
}

func usageError() error {
	fmt.Fprintln(os.Stderr, usage)
	return errUsage
}

func openPane(ctx context.Context, client herdr.Client, entrypoint, target string, popup bool) error {
	args := []string{"plugin", "pane", "open", "--plugin", "herdr-review-loop", "--entrypoint", entrypoint}
	if !popup {
		args = append(args, "--placement", "split", "--direction", "right", "--target-pane", target, "--no-focus", "--env", "HERDR_REVIEW_LOOP_AUTHOR="+target)
	}
	_, err := client.Call(ctx, args...)
	return err
}
func panel(environment herdr.Environment, values config.Values, client herdr.Client) error {
	if existing, won, err := loop.ClaimPanel(environment.StateDir, environment.Context.WorkspaceID, environment.PaneID); err != nil {
		return err
	} else if !won {
		_ = client.PluginPaneFocus(context.Background(), existing.PaneID)
		return nil
	}
	log := loop.Log{StateDir: environment.StateDir}
	var pair struct {
		sync.RWMutex
		author, reviewer, message string
	}
	pair.author = environment.Context.FocusedPaneID
	refreshPair := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		authorText, reviewerText, message := environment.Context.FocusedPaneID, "", ""
		agents, err := client.AgentList(ctx)
		if err != nil {
			message = "no pair: " + err.Error()
		} else if author, ok := herdr.Find(agents, environment.Context.FocusedPaneID); ok {
			authorText = herdr.Describe(author)
			if reviewer, pickErr := herdr.PickReviewer(values, agents, author, nil); pickErr == nil {
				reviewerText = herdr.Describe(reviewer)
			} else {
				message = "no pair: " + pickErr.Error()
			}
		} else {
			message = "no pair: run this from your agent's pane"
		}
		pair.Lock()
		pair.author, pair.reviewer, pair.message = authorText, reviewerText, message
		pair.Unlock()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			refreshPair()
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	refresh := func() ui.PanelState {
		tail, _ := log.Tail()
		held := loop.LiveRun(environment.StateDir)
		state := ui.PanelState{Author: environment.Context.FocusedPaneID, Phase: loop.Phase(tail), Tail: tail, Running: held}
		if !held {
			state.Phase = loop.LastOutcome(tail)
		}
		pair.RLock()
		state.Author, state.Reviewer, state.Message = pair.author, pair.reviewer, pair.message
		pair.RUnlock()
		return state
	}
	return ui.Panel(os.Stdin, os.Stdout, refresh, ui.PanelActions{
		Review: func() string {
			executable, err := os.Executable()
			if err != nil {
				return err.Error()
			}
			// re-invoking this same binary, detached, so the loop outlives the panel
			command := exec.Command(executable, "review") //nolint:gosec // the executable is this process
			command.Stdin = nil
			command.Stdout = nil
			command.Stderr = nil
			command.Env = append(os.Environ(), "HERDR_REVIEW_LOOP_AUTHOR="+environment.Context.FocusedPaneID)
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := command.Start(); err != nil {
				return err.Error()
			}
			go func() { _ = command.Wait() }()
			return "review started"
		},
		Stop: func() string {
			if err := stopRun(client, environment); err != nil {
				return err.Error()
			}
			return "review loop canceled"
		},
		Settings: func() string {
			if err := openPane(context.Background(), client, "settings", environment.Context.FocusedPaneID, true); err != nil {
				return err.Error()
			}
			return "opened settings"
		},
		History: func() string {
			if err := openPane(context.Background(), client, "history", environment.Context.FocusedPaneID, true); err != nil {
				return err.Error()
			}
			return "opened history"
		},
		Finish: func() (string, bool) {
			lines, err := finishReview(environment, values)
			if err != nil {
				return err.Error(), false
			}
			return strings.Join(lines, " · "), true
		},
	})
}

// finishReview cleans up and reports; it does not close the panel, because the
// panel is one of its callers. Closing a pane can take its whole process group
// with it, so the caller does that last, once nothing else is left to do.
func finishReview(environment herdr.Environment, values config.Values) ([]string, error) {
	repository, err := environment.Repository()
	if err != nil {
		return nil, err
	}
	result, err := loop.Finish(environment.StateDir, environment.Context.WorkspaceID, repository, values.ReviewFile)
	if err != nil {
		return nil, err
	}
	lines := result.Digest()
	if !result.Empty() {
		_ = loop.Log{StateDir: environment.StateDir}.Write("finished: " + strings.Join(lines, "; "))
	}
	return lines, nil
}

func history(environment herdr.Environment) error {
	ctx := context.Background()
	views := loop.Browse(ctx, environment.StateDir)
	runs := make([]ui.HistoryRun, 0, len(views))
	for _, view := range views {
		item := ui.HistoryRun{Title: runTitle(view)}
		for _, round := range view.Rounds {
			item.Rounds = append(item.Rounds, ui.HistoryRound{Label: roundLabel(round), HasDiff: round.HasDiff()})
		}
		runs = append(runs, item)
	}
	diff := func(repository, base, head string) *exec.Cmd {
		if repository == "" || base == "" || head == "" || base == head {
			return nil
		}
		return exec.Command("git", "-C", repository, "diff", base, head) //nolint:gosec // repository and commits come from our own records
	}
	return ui.History(os.Stdin, os.Stdout, runs, ui.HistoryActions{
		Findings: func(run, round int) *exec.Cmd {
			view, ok := historyRound(views, run, round)
			if !ok {
				return nil
			}
			return pager(view.Verdict)
		},
		RoundDiff: func(run, round int) *exec.Cmd {
			runView, ok := historyRun(views, run)
			if !ok {
				return nil
			}
			roundView, ok := historyRound(views, run, round)
			if !ok {
				return nil
			}
			return diff(runView.Record.Repository, roundView.Base, roundView.Commit)
		},
		RunDiff: func(run int) *exec.Cmd {
			view, ok := historyRun(views, run)
			if !ok {
				return nil
			}
			return diff(view.Record.Repository, view.Baseline(), view.Last())
		},
		Restore: func(run, round int) string {
			runView, ok := historyRun(views, run)
			if !ok {
				return "no archived rounds"
			}
			roundView, ok := historyRound(views, run, round)
			if !ok {
				return "no archived rounds"
			}
			commit := roundView.Commit
			if commit == "" {
				return "no checkpoint for this round"
			}
			return fmt.Sprintf("git -C %q restore --source=%s --worktree -- .   (files created later are not removed)", runView.Record.Repository, commit)
		},
	})
}

func historyRun(views []loop.RunView, run int) (loop.RunView, bool) {
	if run < 0 || run >= len(views) {
		return loop.RunView{}, false
	}
	return views[run], true
}

func historyRound(views []loop.RunView, run, round int) (loop.RoundView, bool) {
	view, ok := historyRun(views, run)
	if !ok || round < 0 || round >= len(view.Rounds) {
		return loop.RoundView{}, false
	}
	return view.Rounds[round], true
}

func runTitle(view loop.RunView) string {
	title := view.Record.Started.Local().Format("2006-01-02 15:04")
	if view.Record.Repository != "" {
		title += " · " + view.Record.Repository
	}
	if view.Record.Author != "" {
		title += " · " + view.Record.Author + " ← " + view.Record.Reviewer
	}
	title += fmt.Sprintf(" · %d round(s)", len(view.Rounds))
	if view.Record.Outcome != "" {
		title += " · " + view.Record.Outcome
	}
	return title
}

func roundLabel(round loop.RoundView) string {
	if round.Clean {
		return fmt.Sprintf("round %d   clean", round.Number)
	}
	return fmt.Sprintf("round %d   %d finding(s)", round.Number, round.Findings)
}

// pager resolves the user's own pager so findings scroll and search the way
// everything else on their terminal does. git picks its own for diffs.
func pager(path string) *exec.Cmd {
	if path == "" {
		return nil
	}
	command := os.Getenv("PAGER")
	if command == "" {
		if _, err := exec.LookPath("less"); err == nil {
			command = "less -R"
		} else {
			command = "cat"
		}
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		if _, err := exec.LookPath("less"); err == nil {
			fields = []string{"less", "-R"}
		} else {
			fields = []string{"cat"}
		}
	}
	return exec.Command(fields[0], append(fields[1:], path)...) //nolint:gosec // the user's own PAGER, run on their behalf
}

func settingsStatus(stateDir string) string {
	record, err := loop.Holder(stateDir)
	if err != nil || !loop.StillTheHolder(record) {
		return ""
	}
	return fmt.Sprintf("review loop running (pid %d, since %s)", record.PID, record.Started.Local().Format("15:04"))
}

func stopRun(client herdr.Client, environment herdr.Environment) error {
	record, held, err := loop.WaitHolder(environment.StateDir, time.Second)
	if err != nil {
		return err
	}
	if !held {
		fmt.Println("no review loop is running")
		return nil
	}
	if !loop.StillTheHolder(record) {
		fmt.Println("no review loop is running")
		return nil
	}
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", record.PID, err)
	}
	if loop.StillTheHolder(record) {
		_ = process.Signal(syscall.SIGTERM)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !loop.StillTheHolder(record) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if loop.StillTheHolder(record) {
		_ = process.Signal(syscall.SIGKILL)
		time.Sleep(time.Second)
	}
	_ = client.WorkspaceReportMetadata(context.Background(), record.Workspace, "", true)
	log := loop.Log{StateDir: environment.StateDir, Output: os.Stdout}
	if loop.StillTheHolder(record) {
		_ = log.Write("review loop process survived cancellation")
		_ = client.NotificationShow(context.Background(), "Review loop stopped", "review loop process survived cancellation")
		return fmt.Errorf("review loop process survived cancellation")
	}
	_ = log.Write("canceled")
	_ = client.NotificationShow(context.Background(), "Review loop canceled", "review loop canceled")
	fmt.Println("review loop canceled")
	return nil
}
