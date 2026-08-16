package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
	"github.com/mikhail-angelov/herdr-review-loop/internal/loop"
)

// configCommand explains the configuration a run would use and where every part of it came from.
// It runs no review and writes nothing: a surprising resolution should cost one command to see.
func configCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	resolution := app.resolve(nil)
	lines := []string{
		"profile:  " + describeProfile(resolution.Profile),
		"scope:    " + resolution.Values.Scope,
	}
	lines = append(lines, "pair:     "+describePair(app))
	if project := projectLayer(app.environment); project != "" {
		lines = append(lines, "project:  "+config.Path(project))
	}
	lines = append(lines, "user:     "+config.Path(app.environment.ConfigDir), "", "settings")
	width := 0
	for _, field := range config.Fields() {
		width = max(width, len(field.Key))
	}
	for _, field := range config.Fields() {
		value := config.Show(field.Key, resolution.Values)
		if value == "" {
			value = "unset"
		}
		lines = append(lines, fmt.Sprintf("  %-*s  %-22s %s", width, field.Key, value, resolution.Layers[field.Key]))
	}
	reviewerKind := pairedReviewerKind(app)
	lines = append(lines, "", "rounds  ("+resolution.Layers["rounds"]+")")
	for round := 1; round <= min(resolution.Values.MaxIterations, max(len(resolution.Values.Rounds), 1)); round++ {
		policy := loop.Policy(resolution.Values.Rounds, round)
		command := loop.DescribeCommand(reviewerKind, resolution.Values.ReviewCommand, policy)
		lines = append(lines, fmt.Sprintf("  %d  %s — %s", round, command, policy.Describe()))
	}
	lines = append(lines, "", "review commands")
	lines = append(lines, describeReviewCommands(resolution.Values)...)
	lines = append(lines, "", "profiles")
	for _, profile := range config.Profiles(config.Sources{User: app.environment.ConfigDir, Project: projectLayer(app.environment)}) {
		lines = append(lines, "  "+describeProfile(profile))
	}
	for _, warning := range resolution.Warnings {
		lines = append(lines, "", "warning: "+warning)
	}
	if _, err := fmt.Fprintln(os.Stdout, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("failed to write the resolved configuration: %w", err)
	}
	return nil
}

func describeProfile(profile config.Profile) string {
	text := profile.Name + " (" + profile.Layer + ")"
	if profile.Layer == "" {
		text = profile.Name + " (not found)"
	}
	if profile.Path != "" && profile.Layer != config.LayerDefault {
		text += "  " + profile.Path
	}
	if profile.Description != "" {
		text += "  — " + profile.Description
	}
	return text
}

// describeReviewCommands names the command every kind the loop knows would run for round 1, so
// the same resolution the run uses is what a reader sees. Kinds with no entry are not listed
// individually — that they fall back is stated once.
func describeReviewCommands(values config.Values) []string {
	policy := loop.Policy(values.Rounds, 1)
	kinds := loop.Kinds(values.ReviewCommand)
	lines := make([]string, 0, len(kinds)+1)
	for _, kind := range kinds {
		lines = append(lines, fmt.Sprintf("  %-10s %s", kind, loop.DescribeCommand(kind, values.ReviewCommand, policy)))
	}
	return append(lines, "  every other kind uses the built-in review prompt")
}

// pairedReviewerKind is whose review command would actually run. It falls back to naming no kind,
// which resolves to the built-in prompt — the honest answer when there is no pair to ask.
func pairedReviewerKind(app application) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	agents, err := app.client.AgentList(ctx)
	if err != nil {
		return ""
	}
	author, ok := herdr.Find(agents, app.environment.Context.FocusedPaneID)
	if !ok {
		return ""
	}
	reviewer, err := herdr.PickReviewer(app.values, agents, author, nil)
	if err != nil {
		return ""
	}
	return reviewer.Kind
}

// describePair resolves who would write and who would review, which is the one part of the
// configuration that depends on what is running rather than on what is on disk.
func describePair(app application) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	agents, err := app.client.AgentList(ctx)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	author, ok := herdr.Find(agents, app.environment.Context.FocusedPaneID)
	if !ok {
		return "unavailable: run this from your agent's pane"
	}
	reviewer, err := herdr.PickReviewer(app.values, agents, author, nil)
	if err != nil {
		return herdr.Describe(author) + " → " + err.Error()
	}
	return herdr.Describe(author) + " → " + herdr.Describe(reviewer)
}

// initCommand copies the winning profile down into the project and writes a config.json holding
// only what the user is actually overriding. It never freezes the resolved state: a project that
// committed today's defaults would silently stop tracking tomorrow's.
func initCommand(app application, args []string) error {
	if len(args) != 0 {
		return usageError()
	}
	project := projectLayer(app.environment)
	if project == "" {
		return loop.Exit(loop.ExitTool, errors.New("no workspace directory to initialize; run this from your agent's pane"))
	}
	resolution := app.resolve(nil)
	if err := os.MkdirAll(filepath.Join(project, config.ProfileSubdir), 0o700); err != nil {
		return fmt.Errorf("failed to create %s: %w", project, err)
	}
	written := []string{}
	settings, err := projectSettings(resolution)
	if err != nil {
		return err
	}
	created, err := writeIfAbsent(config.Path(project), settings)
	if err != nil {
		return err
	}
	written = append(written, report(config.Path(project), created))

	example := filepath.Join(project, "config.example.md")
	created, err = writeIfAbsent(example, config.Example())
	if err != nil {
		return err
	}
	written = append(written, report(example, created))

	name := resolution.Values.Profile
	data, profile, found := config.ProfileFile(config.Sources{User: app.environment.ConfigDir, Project: project}, name)
	if !found {
		return fmt.Errorf("profile %q was not found in any layer, so there is nothing to copy down", name)
	}
	if profile.Layer != config.LayerProject {
		path := filepath.Join(project, config.ProfileSubdir, name+".json")
		if created, err = writeIfAbsent(path, string(data)); err != nil {
			return err
		}
		written = append(written, report(path, created))
	}
	if _, err := fmt.Fprintln(os.Stdout, strings.Join(written, "\n")); err != nil {
		return fmt.Errorf("failed to write the init report: %w", err)
	}
	return nil
}

// projectSettings is the config.json init writes: an object holding the keys the user's own layer
// overrides and nothing else, so the project inherits every default it has no opinion about.
func projectSettings(resolution config.Resolution) (string, error) {
	values := config.Defaults()
	for _, field := range config.Fields() {
		if resolution.Layers[field.Key] != config.LayerUser {
			continue
		}
		parsed, err := config.Parse(field.Key, config.Show(field.Key, resolution.Values))
		if err != nil {
			continue
		}
		if err := config.Apply(&values, field.Key, parsed); err != nil {
			return "", fmt.Errorf("failed to carry %s down into the project: %w", field.Key, err)
		}
	}
	// rounds and review_command have no pane field, so the loop above cannot reach them; without
	// this the project inherits the built-in policy rather than the one the user is running under
	if resolution.Layers["rounds"] == config.LayerUser {
		values.Rounds = resolution.Values.Rounds
	}
	if resolution.Layers["review_command"] == config.LayerUser {
		values.ReviewCommand = resolution.Values.ReviewCommand
	}
	return config.Render(values), nil
}

// writeIfAbsent never replaces a file the project already committed: init is a starting point, and
// running it twice must not be how a policy gets lost.
func writeIfAbsent(path, contents string) (bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the path is built from the plugin's own directory names
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to create %s: %w", path, err)
	}
	_, writeErr := file.WriteString(contents)
	closeErr := file.Close()
	if writeErr != nil {
		return false, fmt.Errorf("failed to write %s: %w", path, writeErr)
	}
	if closeErr != nil {
		return false, fmt.Errorf("failed to close %s: %w", path, closeErr)
	}
	return true, nil
}

func report(path string, created bool) string {
	if created {
		return "wrote " + path
	}
	return "kept  " + path + " (already present)"
}
