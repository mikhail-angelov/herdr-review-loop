package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
	"github.com/mikhail-angelov/herdr-review-loop/internal/loop"
	"github.com/mikhail-angelov/herdr-review-loop/internal/ui"
)

// version is stamped at build time from herdr-plugin.toml, which is the only place the plugin's
// version is written. The default is deliberately not a version number: a binary that was built
// without the stamp must fail the manifest check and be replaced, never pass for the release it
// happens to sit next to.
var version = "dev"

// main maps a failure to the exit code the loop decided on. Every code but 2 is chosen where the
// outcome is known; an error that names none is a tool error, which is also what a usage error is.
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(loop.ExitCode(err))
	}
}

var errUsage = errors.New("usage error")

const usage = "usage: herdr-review-loop <review [--dry-run] [--one-shot] [--profile NAME] [--scope SPEC]|show [--run ID] [--round N] [--format md|json]|view <findings|diff|log>|config|init|stop|finish|open-panel|open-settings|open-history|open-findings|open-diff|open-log|panel|settings|history|version|help>"

// application is everything a command needs after the environment has been loaded, assembled once
// so the command table below stays a table. resolve is kept alongside the already-resolved values
// because only `review` has an invocation layer, and it knows its own flags after dispatch.
type application struct {
	environment herdr.Environment
	values      config.Values
	client      herdr.Client
	warnings    []string
	resolve     func(invocation map[string]any) config.Resolution
}

// commands is the whole CLI surface. Dispatching through a table keeps each verb's behavior in one
// named function instead of one long switch.
var commands = map[string]func(application, []string) error{
	"review":        reviewCommand,
	"show":          showCommand,
	"view":          viewCommand,
	"config":        configCommand,
	"init":          initCommand,
	"stop":          stopCommand,
	"finish":        finishCommand,
	"settings":      settingsCommand,
	"open-panel":    openPanelCommand,
	"open-settings": openSettingsCommand,
	"open-history":  openHistoryCommand,
	"open-findings": openPaneCommand("findings"),
	"open-diff":     openPaneCommand("diff"),
	"open-log":      openPaneCommand("log"),
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
	resolve := func(invocation map[string]any) config.Resolution {
		return config.Resolve(config.Sources{User: environment.ConfigDir, Project: projectLayer(environment), Invocation: invocation})
	}
	resolution := resolve(nil)
	for _, warning := range resolution.Warnings {
		fmt.Fprintln(os.Stderr, "herdr-review-loop:", warning)
	}
	return command(application{
		environment: environment,
		values:      resolution.Values,
		client:      herdr.NewClient(environment.Binary),
		warnings:    resolution.Warnings,
		resolve:     resolve,
	}, rest)
}

// projectLayer is the directory a project commits its review-loop configuration to. Outside a
// workspace there is no project layer, which is not an error: the layers below it are complete.
func projectLayer(environment herdr.Environment) string {
	repository, err := environment.Repository()
	if err != nil {
		return ""
	}
	return filepath.Join(repository, loop.PluginDir)
}

func reviewCommand(app application, args []string) error {
	dryRun, oneShot, invocation, err := reviewArguments(args)
	if err != nil {
		return err
	}
	resolution := app.resolve(invocation)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := loop.Log{StateDir: app.environment.StateDir, Output: os.Stdout}
	if !dryRun {
		for _, warning := range resolution.Warnings {
			_ = log.Write(warning)
		}
	}
	run := loop.Run{Client: app.client, Config: resolution.Values, Profile: resolution.Profile, Environment: app.environment, Log: log, Version: version, OneShot: oneShot}
	err = run.Execute(ctx, dryRun)
	if err != nil && !dryRun {
		_ = log.Write(err.Error())
	}
	return err
}

// reviewArguments separates the two switches that change what the run does from the settings that
// merely form the invocation layer, so a flag and the config.json key it overrides stay one thing.
func reviewArguments(args []string) (dryRun, oneShot bool, invocation map[string]any, err error) {
	invocation = map[string]any{}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--dry-run":
			dryRun = true
			continue
		case "--one-shot":
			oneShot = true
			continue
		case "--profile", "--scope":
			if index+1 >= len(args) || args[index+1] == "" {
				return false, false, nil, usageError()
			}
			invocation[strings.TrimPrefix(args[index], "--")] = args[index+1]
			index++
		default:
			return false, false, nil, usageError()
		}
	}
	return dryRun, oneShot, invocation, nil
}

// showCommand renders one archived round. It is the only way a run's findings become markdown, and
// the history pane calls the same renderer, so there is nothing to drift out of sync with.
func showCommand(app application, args []string) error {
	runID, round, format, err := showArguments(args)
	if err != nil {
		return err
	}
	if runID == "" {
		repository, repoErr := app.environment.Repository()
		if repoErr != nil {
			return repoErr
		}
		record, found := loop.LatestRun(app.environment.StateDir, repository)
		if !found {
			return errors.New("no recorded runs for this repository")
		}
		runID = record.ID
	}
	rounds := loop.ArchivedRounds(app.environment.StateDir, runID)
	if len(rounds) == 0 {
		return fmt.Errorf("run %s has no archived rounds", runID)
	}
	if round == 0 {
		round = rounds[len(rounds)-1]
	}
	document, found := loop.LoadRound(app.environment.StateDir, runID, round)
	if !found {
		return fmt.Errorf("run %s has no round %d", runID, round)
	}
	text := loop.RenderMarkdown(document)
	if format == "json" {
		if text, err = loop.RenderJSON(document); err != nil {
			return err
		}
	}
	if _, err := os.Stdout.WriteString(text); err != nil {
		return fmt.Errorf("failed to write the rendered round: %w", err)
	}
	return nil
}

func showArguments(args []string) (runID string, round int, format string, err error) {
	format = "md"
	for index := 0; index < len(args); index++ {
		value := ""
		if index+1 < len(args) {
			value = args[index+1]
		}
		switch args[index] {
		case "--run":
			runID = value
		case "--round":
			if round, err = strconv.Atoi(value); err != nil || round < 1 {
				return "", 0, "", fmt.Errorf("--round expects a round number: %w", errUsage)
			}
		case "--format":
			if value != "md" && value != "json" {
				return "", 0, "", fmt.Errorf("--format expects md or json: %w", errUsage)
			}
			format = value
		default:
			return "", 0, "", usageError()
		}
		if value == "" {
			return "", 0, "", usageError()
		}
		index++
	}
	return runID, round, format, nil
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
	lines, err := finishReview(app.environment)
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
	return openPane(context.Background(), app.client, app.environment, "panel")
}

// openPaneCommand is the action behind a pane the user opens from Herdr's own menus, as opposed to
// from the panel. Every one of them is a popup opened focused on what it shows.
func openPaneCommand(entrypoint string) func(application, []string) error {
	return func(app application, args []string) error {
		if len(args) != 0 {
			return usageError()
		}
		return openPane(context.Background(), app.client, app.environment, entrypoint)
	}
}

func openSettingsCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return openPane(context.Background(), app.client, app.environment, "settings")
}

func openHistoryCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return openPane(context.Background(), app.client, app.environment, "history")
}

func panelCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return panel(app.environment, app.client, app.resolve)
}

func historyCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	return history(app.environment)
}

// startReview re-invokes this same binary, detached, so the loop outlives the panel that started
// it and closing the pane cancels nothing.
func startReview(environment herdr.Environment, argument string) string {
	executable, err := os.Executable()
	if err != nil {
		return err.Error()
	}
	arguments := []string{"review"}
	if argument != "" {
		arguments = append(arguments, argument)
	}
	command := exec.Command(executable, arguments...) //nolint:gosec // the executable is this process
	command.Stdin, command.Stdout, command.Stderr = nil, nil, nil
	command.Env = append(os.Environ(), "HERDR_REVIEW_LOOP_AUTHOR="+environment.Context.FocusedPaneID)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err.Error()
	}
	go func() { _ = command.Wait() }()
	if argument == "--one-shot" {
		return "one-shot review started"
	}
	return "review started"
}

// answerPending carries one choice to the boundary the loop is waiting at. The request is read
// back here rather than held by the panel, so a choice always answers what is actually waiting.
func answerPending(environment herdr.Environment, action, text string) string {
	repository, err := environment.Repository()
	if err != nil {
		return err.Error()
	}
	pending, waiting := loop.PendingRequest(environment.StateDir, environment.Context.WorkspaceID, repository)
	if !waiting {
		return "nothing is waiting for a choice"
	}
	if err := loop.Answer(environment.StateDir, pending, action, text); err != nil {
		return err.Error()
	}
	return "sent: " + action
}

// togglePause asks the loop to stop at its next round boundary, or withdraws that request. It is
// honored between two turns, so a pause never interrupts an agent mid-answer.
func togglePause(environment herdr.Environment) string {
	repository, err := environment.Repository()
	if err != nil {
		return err.Error()
	}
	paused := loop.Paused(environment.StateDir, environment.Context.WorkspaceID, repository)
	if err := loop.SetPause(environment.StateDir, environment.Context.WorkspaceID, repository, !paused); err != nil {
		return err.Error()
	}
	if paused {
		return "pause withdrawn"
	}
	return "pausing at the next round boundary"
}

// feedLines carries the run's events to the panel with a level attached. The loop names what
// happened and the panel decides how loud that is; this is the one place the two vocabularies meet.
func feedLines(feed loop.Feed) []ui.FeedLine {
	lines := make([]ui.FeedLine, 0, len(feed.Lines))
	for _, line := range feed.Lines {
		lines = append(lines, ui.FeedLine{Text: line.Text, Level: feedLevel(line.Event)})
	}
	return lines
}

func feedLevel(event string) string {
	switch event {
	case loop.EventStall, loop.EventTimeout, loop.EventBlocked, loop.EventDegraded, loop.EventCanceled:
		return ui.LevelBad
	case loop.EventRetry, loop.EventParseFallback, loop.EventFiltered, loop.EventPaused:
		return ui.LevelWarn
	case loop.EventPhaseDone, loop.EventResumed:
		return ui.LevelGood
	default:
		return ui.LevelNormal
	}
}

// panelPending turns the loop's boundary into what the panel shows and which keys it offers.
func panelPending(pending loop.Pending) (kind, phase string) {
	switch pending.Kind {
	case loop.PendingOneShot:
		return ui.PendingOneShot, "one-shot review ready — choose what the author does"
	case loop.PendingGateReview:
		return ui.PendingGate, fmt.Sprintf("paused after the round %d review — nothing is changed yet", pending.Round)
	case loop.PendingGateRound:
		return ui.PendingGate, fmt.Sprintf("paused after round %d — the changes are in your tree", pending.Round)
	default:
		return "", ""
	}
}

func usageError() error {
	fmt.Fprintln(os.Stderr, usage)
	return errUsage
}

// panePlacement is where each pane belongs, named once so the panel's keys and Herdr's own action
// menu open the same thing the same way. Findings and a diff are read rather than glanced at, so
// they take a whole tab; settings and history are answered and dismissed, so they are popups; the
// panel is the split beside the author, because it is the one pane nobody asked for.
var panePlacement = map[string]string{
	"panel":    "split",
	"settings": "popup",
	"history":  "popup",
	"findings": "tab",
	"diff":     "tab",
	"log":      "tab",
}

// openPane opens one of the plugin's panes. Everything the user asked for is opened focused; the
// panel, which the loop opens on its own, is the one thing that must not take their place.
func openPane(ctx context.Context, client herdr.Client, environment herdr.Environment, entrypoint string) error {
	target := environment.Context.FocusedPaneID
	opening := herdr.PaneOpening{Entrypoint: entrypoint, Focus: true}
	switch panePlacement[entrypoint] {
	case "split":
		opening.Placement, opening.Direction, opening.Target, opening.Focus = "split", "right", target, false
		opening.Env = []string{"HERDR_REVIEW_LOOP_AUTHOR=" + target}
	case "tab":
		opening.Placement, opening.Workspace = "tab", environment.Context.WorkspaceID
	}
	pane, err := client.PluginPaneOpen(ctx, opening)
	if err != nil || !opening.Focus {
		return err
	}
	// the open flag is answered while the pane is still being built, so the user is taken there
	// once there is something to name — and what is named decides whether they actually move: a
	// pane in a tab of its own is reached by focusing that tab, because the terminal changes the
	// tab it is showing only for a tab that was focused, not for a pane that happens to sit in one
	if pane.TabID != "" && panePlacement[entrypoint] == "tab" {
		return client.TabFocus(ctx, pane.TabID)
	}
	return client.PluginPaneFocus(ctx, pane.ID)
}

// viewCommand is the body of the findings and diff panes: it renders what the newest run has
// recorded and hands it to the user's pager, which is what keeps the pane open until they leave it.
func viewCommand(app application, args []string) error {
	if len(args) != 1 {
		return usageError()
	}
	repository, err := app.environment.Repository()
	if err != nil {
		return err
	}
	var text string
	switch args[0] {
	case "findings":
		text = latestFindings(app.environment, repository)
	case "diff":
		text, err = latestDiff(context.Background(), app.environment, repository, colored())
	case "log":
		text = loop.LogDocument(app.environment.StateDir, repository, paneStyle())
	default:
		return usageError()
	}
	if err != nil {
		return err
	}
	command := pager(text)
	if command == nil {
		return nil
	}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("failed to page the output: %w", err)
	}
	return nil
}

// latestFindings renders the newest round this repository has archived, running or finished. The
// archive is written as each phase completes, so a round under review is readable while it runs.
func latestFindings(environment herdr.Environment, repository string) string {
	record, found := loop.LatestRun(environment.StateDir, repository)
	if !found {
		return "no recorded runs for this repository yet.\n"
	}
	rounds := loop.ArchivedRounds(environment.StateDir, record.ID)
	if len(rounds) == 0 {
		return "the current run has not finished a review yet.\n"
	}
	document, ok := loop.LoadRound(environment.StateDir, record.ID, rounds[len(rounds)-1])
	if !ok {
		return "the newest round could not be read.\n"
	}
	return loop.Render(document, paneStyle())
}

// latestDiff shows what the loop has changed most recently: the last completed round when there is
// one, and otherwise the working tree against the checkpoint the round started from.
func latestDiff(ctx context.Context, environment herdr.Environment, repository string, color bool) (string, error) {
	base, head, found := loop.LatestChange(ctx, environment.StateDir, repository)
	if !found {
		return "no checkpoint to compare against; the run recorded none.\n", nil
	}
	// git turns its colors off the moment it is not writing to a terminal, and this diff is always
	// written into a pager: without asking, the pane would show a diff no one could read at a
	// glance
	args := []string{"-C", repository, "diff", diffColor(color), base}
	if head != "" {
		args = append(args, head)
	}
	output, err := exec.Command("git", args...).Output() //nolint:gosec // the repository and commits come from our own records
	if err != nil {
		return "", fmt.Errorf("failed to diff %s: %w", base, err)
	}
	if strings.TrimSpace(string(output)) == "" {
		return "no changes recorded since " + base[:min(len(base), 8)] + ".\n", nil
	}
	return string(output), nil
}

// panel re-resolves the configuration on every refresh rather than holding the values it started
// with: the settings pane writes that file while the panel is open, and a panel showing the scope
// of a run nobody would start any more is worse than one showing no scope at all.
func panel(environment herdr.Environment, client herdr.Client, resolve func(map[string]any) config.Resolution) error {
	if existing, won, err := loop.ClaimPanel(environment.StateDir, environment.Context.WorkspaceID, environment.PaneID); err != nil {
		return err
	} else if !won {
		_ = client.PluginPaneFocus(context.Background(), existing.PaneID)
		return nil
	}
	// events wake the panel the moment the loop reports a phase or the author's pane changes
	// state, so what it shows is what is happening rather than what was happening a tick ago
	wake := make(chan struct{}, 1)
	nudge := make(chan struct{}, 1)
	signalTo := func(channel chan struct{}) {
		select {
		case channel <- struct{}{}:
		default:
		}
	}
	watching, stopWatching := context.WithCancel(context.Background())
	defer stopWatching()
	go func() {
		notices := herdr.Watch(watching, environment.Socket, []herdr.Subscription{
			{Type: herdr.SubscriptionWorkspaceMetadata},
			{Type: herdr.SubscriptionAgentStatus, PaneID: environment.Context.FocusedPaneID},
		})
		for range notices {
			signalTo(nudge)
			signalTo(wake)
		}
	}()
	var pair struct {
		sync.RWMutex
		author, reviewer, message    string
		authorStatus, reviewerStatus string
		scope, changes, spec         string
	}
	pair.author = environment.Context.FocusedPaneID
	refreshPair := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		values := resolve(nil).Values
		scopeText, changesText := "", ""
		spec := values.Scope
		if repository, err := environment.Repository(); err == nil {
			scopeText, changesText = loop.PanelScope(ctx, repository, values.Scope)
		}
		authorText, reviewerText, message := environment.Context.FocusedPaneID, "", ""
		authorStatus, reviewerStatus := "", ""
		agents, err := client.AgentList(ctx)
		if err != nil {
			message = "no pair: " + err.Error()
		} else if author, ok := herdr.Find(agents, environment.Context.FocusedPaneID); ok {
			authorText, authorStatus = herdr.Describe(author), author.Status
			if reviewer, pickErr := herdr.PickReviewer(values, agents, author, nil); pickErr == nil {
				reviewerText, reviewerStatus = herdr.Describe(reviewer), reviewer.Status
			} else {
				message = "no pair: " + pickErr.Error()
			}
		} else {
			message = "no pair: run this from your agent's pane"
		}
		pair.Lock()
		pair.author, pair.reviewer, pair.message = authorText, reviewerText, message
		pair.authorStatus, pair.reviewerStatus = authorStatus, reviewerStatus
		pair.scope, pair.changes, pair.spec = scopeText, changesText, spec
		pair.Unlock()
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			refreshPair()
			signalTo(wake)
			select {
			case <-done:
				return
			case <-nudge:
			case <-tick.C:
			}
		}
	}()
	refresh := func() ui.PanelState {
		owner, live := loop.LiveRun(environment.StateDir)
		held := live && owner == environment.Context.WorkspaceID
		repository, repositoryErr := environment.Repository()
		feed := loop.LatestFeed(environment.StateDir, repository, 200)
		state := ui.PanelState{Author: environment.Context.FocusedPaneID, Phase: feed.Phase, Events: feedLines(feed), Running: held}
		if !held {
			state.Phase = feed.Outcome
		}
		// one loop runs per state directory, so a panel elsewhere is not idle — it is waiting for
		// a turn, and saying so beats offering a review that would fail on the lock
		if live && !held {
			state.Busy, state.Phase = true, "a review loop is running in another space"
		}
		// a boundary is only real while its run is: a loop killed outright leaves its request
		// behind, and a panel offering choices nobody is waiting for is worse than one offering none
		if repositoryErr == nil && held {
			state.Paused = loop.Paused(environment.StateDir, environment.Context.WorkspaceID, repository)
			if pending, waiting := loop.PendingRequest(environment.StateDir, environment.Context.WorkspaceID, repository); waiting {
				state.Pending, state.Phase = panelPending(pending)
				state.Summary = pending.Summary
			}
		}
		pair.RLock()
		state.Author, state.Reviewer, state.Message = pair.author, pair.reviewer, pair.message
		state.AuthorStatus, state.ReviewerStatus = pair.authorStatus, pair.reviewerStatus
		state.Scope, state.Changes = pair.scope, pair.changes
		pair.RUnlock()
		return state
	}
	open := func(entrypoint string) func() string {
		return func() string {
			if err := openPane(context.Background(), client, environment, entrypoint); err != nil {
				return err.Error()
			}
			return "opened " + entrypoint
		}
	}
	scopes := func() []ui.ScopeOption {
		pair.RLock()
		spec := pair.spec
		pair.RUnlock()
		options := make([]ui.ScopeOption, 0, 5)
		for _, choice := range []struct{ label, spec string }{
			{"auto — decide for me", config.ScopeAuto},
			{"worktree — uncommitted changes", config.ScopeWorktree},
			{"branch — against the branch it came from", config.ScopeBranch},
		} {
			options = append(options, ui.ScopeOption{Label: choice.label, Spec: choice.spec, Current: choice.spec == spec})
		}
		typed := ui.ScopeOption{Label: "type one — branch:<ref> or text:<path>"}
		// a scope with an argument cannot be a fixed row, so the row that asks for one carries the
		// current mark whenever the setting is one of those
		typed.Current = !slices.ContainsFunc(options, func(option ui.ScopeOption) bool { return option.Current })
		return append(options, typed)
	}
	setScope := func(spec string) string {
		values := resolve(nil).Values
		parsed, err := config.Parse("scope", spec)
		if err != nil {
			return "scope: " + err.Error()
		}
		if err := config.Apply(&values, "scope", parsed); err != nil {
			return "scope: " + err.Error()
		}
		if _, err := config.Save(environment.ConfigDir, values); err != nil {
			return err.Error()
		}
		// the scope line is read from the configuration by the background refresh, so ask for one
		// now rather than leaving the panel showing what was true a moment ago
		signalTo(nudge)
		return "scope → " + spec
	}
	return ui.Panel(os.Stdin, os.Stdout, refresh, wake, ui.PanelActions{
		Review:   func() string { return startReview(environment, "") },
		OneShot:  func() string { return startReview(environment, "--one-shot") },
		Answer:   func(action, text string) string { return answerPending(environment, action, text) },
		Pause:    func() string { return togglePause(environment) },
		Findings: open("findings"),
		Diff:     open("diff"),
		Log:      open("log"),
		Scopes:   scopes,
		SetScope: setScope,
		Stop: func() string {
			if err := stopRun(client, environment); err != nil {
				return err.Error()
			}
			return "review loop canceled"
		},
		Settings: open("settings"),
		History:  open("history"),
		Finish: func() (string, bool) {
			lines, err := finishReview(environment)
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
func finishReview(environment herdr.Environment) ([]string, error) {
	repository, err := environment.Repository()
	if err != nil {
		return nil, err
	}
	result, err := loop.Finish(environment.StateDir, environment.Context.WorkspaceID, repository)
	if err != nil {
		return nil, err
	}
	if !result.Empty() {
		_ = loop.Log{StateDir: environment.StateDir}.Write("finished: " + result.Summarize())
	}
	return result.Digest(), nil
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
		return exec.Command("git", "-C", repository, "diff", diffColor(colored()), base, head) //nolint:gosec // repository and commits come from our own records
	}
	return ui.History(os.Stdin, os.Stdout, runs, ui.HistoryActions{
		Findings: func(run, round int) *exec.Cmd {
			runView, ok := historyRun(views, run)
			if !ok {
				return nil
			}
			roundView, ok := historyRound(views, run, round)
			if !ok {
				return nil
			}
			document, found := loop.LoadRound(environment.StateDir, runView.Record.ID, roundView.Number)
			if !found {
				return nil
			}
			return pager(loop.Render(document, paneStyle()))
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
	switch {
	case round.Clean:
		return fmt.Sprintf("round %d   clean", round.Number)
	case round.Blocked:
		return fmt.Sprintf("round %d   blocked on a question", round.Number)
	default:
		return fmt.Sprintf("round %d   %d finding(s) · %d applied", round.Number, round.Findings, round.Applied)
	}
}

// colored reports whether a pane may use color. NO_COLOR is honored because it is the one
// convention every terminal tool already agrees on, and a pane is the only place this plugin emits
// color at all — `show` stays plain, so a redirected round is the same bytes it always was.
func colored() bool { return os.Getenv("NO_COLOR") == "" }

// diffColor is what git must be told, given that answer. `always` rather than `auto`, because this
// diff is written into a pager and git would see the pipe and give up its colors.
func diffColor(color bool) string {
	if color {
		return "--color=always"
	}
	return "--color=never"
}

// paneStyle is how a round reads in a pane: the severity and the author's verdict carry the colors
// the panel already uses for the same states, and the markdown markers give way to them. Severity
// is the one thing a reader scans a findings pane for, so it is the one thing that is never dim.
func paneStyle() loop.Style {
	if !colored() {
		return loop.MarkdownStyle()
	}
	return loop.Style{
		Title: ui.Bold, Head: ui.Bold, Strong: ui.Bold, Faint: ui.Dim,
		Severity: func(severity string) string {
			switch severity {
			case loop.SeverityHigh:
				return ui.Red(severity)
			case loop.SeverityMedium:
				return ui.Yellow(severity)
			default:
				return ui.Cyan(severity)
			}
		},
		Action: func(action string) string {
			switch action {
			case loop.ActionApplied:
				return ui.Green(action)
			case loop.ActionRejected:
				return ui.Dim(action)
			default:
				return ui.Yellow(action)
			}
		},
		// an event is colored by the same level the panel gives it, through the same table: a
		// stall the panel prints in red must not be a plain line in the log pane beside it
		Event: func(event, text string) string {
			switch feedLevel(event) {
			case ui.LevelBad:
				return ui.Red(text)
			case ui.LevelWarn:
				return ui.Yellow(text)
			case ui.LevelGood:
				return ui.Green(text)
			default:
				return text
			}
		},
	}
}

// pager feeds a rendered round to the user's own pager, so findings scroll and search the way
// everything else on their terminal does. Nothing is written to disk to show it.
func pager(text string) *exec.Cmd {
	if text == "" {
		return nil
	}
	fields := strings.Fields(os.Getenv("PAGER"))
	if len(fields) == 0 {
		if _, err := exec.LookPath("less"); err == nil {
			fields = []string{"less", "-R"}
		} else {
			fields = []string{"cat"}
		}
	}
	command := exec.Command(fields[0], fields[1:]...) //nolint:gosec // the user's own PAGER, run on their behalf
	command.Stdin = strings.NewReader(text)
	return command
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
		_ = client.NotificationShow(context.Background(), "Review loop stopped", "review loop process survived cancellation", "request")
		return fmt.Errorf("review loop process survived cancellation")
	}
	// a run killed outright ran none of its own cleanup, so the boundary it was waiting at is
	// cleared here instead
	loop.ClearControl(environment.StateDir)
	_ = log.Write("canceled")
	_ = client.NotificationShow(context.Background(), "Review loop canceled", "review loop canceled", "request")
	fmt.Println("review loop canceled")
	return nil
}
