package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

const usage = "usage: herdr-review-loop <review [--dry-run]|stop|open-panel|open-settings|panel|settings|version|help>"

func run(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		return errUsage
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return usageError()
		}
		fmt.Println(version)
		return nil
	case "help":
		if len(args) != 1 {
			return usageError()
		}
		fmt.Println(usage)
		return nil
	}
	environment, err := herdr.LoadEnvironment()
	if err != nil {
		return err
	}
	values, warnings := config.Load(environment.ConfigDir)
	for _, warning := range warnings {
		fmt.Fprintln(os.Stderr, "herdr-review-loop:", warning)
	}
	client := herdr.NewClient(environment.Binary)
	switch args[0] {
	case "review":
		dryRun := len(args) == 2 && args[1] == "--dry-run"
		if len(args) != 1 && !dryRun {
			return usageError()
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		log := loop.Log{StateDir: environment.StateDir, Output: os.Stdout}
		if !dryRun {
			for _, warning := range warnings {
				_ = log.Write(warning)
			}
		}
		err := loop.Run{Client: client, Config: values, Environment: environment, Log: log}.Execute(ctx, dryRun)
		if err != nil && !dryRun {
			_ = log.Write(err.Error())
		}
		return err
	case "stop":
		if len(args) != 1 {
			return usageError()
		}
		return stopRun(client, environment)
	case "settings":
		if len(args) != 1 {
			return usageError()
		}
		return ui.Settings(os.Stdin, os.Stdout, environment.ConfigDir, values, func() string {
			return settingsStatus(environment.StateDir)
		}, func() string {
			if err := stopRun(client, environment); err != nil {
				return err.Error()
			}
			return "review loop cancelled"
		})
	case "open-panel":
		if len(args) != 1 {
			return usageError()
		}
		if existing, live := loop.LivePanel(environment.StateDir, environment.Context.WorkspaceID); live {
			return client.PluginPaneFocus(context.Background(), existing.PaneID)
		}
		return openPane(context.Background(), client, "panel", environment.Context.FocusedPaneID, false)
	case "open-settings":
		if len(args) != 1 {
			return usageError()
		}
		return openPane(context.Background(), client, "settings", environment.Context.FocusedPaneID, true)
	case "panel":
		if len(args) != 1 {
			return usageError()
		}
		return panel(environment, values, client)
	default:
		fmt.Fprintln(os.Stderr, usage)
		return errUsage
	}
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
	return ui.Panel(os.Stdin, os.Stdout, refresh, func() string {
		executable, err := os.Executable()
		if err != nil {
			return err.Error()
		}
		command := exec.Command(executable, "review")
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
	}, func() string {
		if err := stopRun(client, environment); err != nil {
			return err.Error()
		}
		return "review loop cancelled"
	}, func() string {
		if err := openPane(context.Background(), client, "settings", environment.Context.FocusedPaneID, true); err != nil {
			return err.Error()
		}
		return "opened settings"
	})
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
		return err
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
	_ = log.Write("cancelled")
	_ = client.NotificationShow(context.Background(), "Review loop cancelled", "review loop cancelled")
	fmt.Println("review loop cancelled")
	return nil
}
