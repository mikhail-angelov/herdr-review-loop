package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
	"github.com/mikhail-angelov/herdr-review-loop/internal/loop"
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
			return errUsage
		}
		fmt.Println(version)
		return nil
	case "help":
		if len(args) != 1 {
			return errUsage
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
			return errUsage
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return loop.Run{Client: client, Config: values, Environment: environment, Log: loop.Log{StateDir: environment.StateDir, Output: os.Stdout}}.Execute(ctx, dryRun)
	case "stop":
		if len(args) != 1 {
			return errUsage
		}
		return stopRun(client, environment)
	case "settings":
		if len(args) != 1 {
			return errUsage
		}
		return printSettings(environment.ConfigDir, values)
	case "open-panel":
		if len(args) != 1 {
			return errUsage
		}
		return openPane(context.Background(), client, "panel", environment.Context.FocusedPaneID, false)
	case "open-settings":
		if len(args) != 1 {
			return errUsage
		}
		return openPane(context.Background(), client, "settings", environment.Context.FocusedPaneID, true)
	case "panel":
		if len(args) != 1 {
			return errUsage
		}
		return panel(environment.StateDir)
	default:
		fmt.Fprintln(os.Stderr, usage)
		return errUsage
	}
}

func printSettings(directory string, values config.Values) error {
	fmt.Println(config.Path(directory))
	for _, field := range config.Fields() {
		fmt.Printf("%s: %s\n", field.Label, config.Show(field.Key, values))
	}
	return nil
}
func openPane(ctx context.Context, client herdr.Client, entrypoint, target string, popup bool) error {
	args := []string{"plugin", "pane", "open", "--plugin", "herdr-review-loop", "--entrypoint", entrypoint}
	if popup {
		args = append(args, "--placement", "popup")
	} else {
		args = append(args, "--placement", "split", "--direction", "right", "--target-pane", target, "--no-focus")
	}
	_, err := client.Call(ctx, args...)
	return err
}
func panel(stateDir string) error {
	log := loop.Log{StateDir: stateDir}
	tail, err := log.Tail()
	if err != nil {
		return err
	}
	if tail == "" {
		tail = "idle"
	}
	fmt.Print(tail)
	return nil
}

func stopRun(client herdr.Client, environment herdr.Environment) error {
	record, err := loop.Holder(environment.StateDir)
	if os.IsNotExist(err) {
		fmt.Println("no review loop is running")
		return nil
	}
	if err != nil {
		return fmt.Errorf("a review loop is starting or its record is unreadable — try again")
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
	if loop.StillTheHolder(record) {
		return fmt.Errorf("review loop process survived cancellation")
	}
	fmt.Println("review loop cancelled")
	return nil
}
