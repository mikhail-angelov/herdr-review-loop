package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
	"github.com/mikhail-angelov/herdr-review-loop/internal/loop"
)

// newApplication wires a command against two throwaway directories and a Herdr binary that is not
// there, which is the only part of the environment `config` and `init` do not need.
func newApplication(t *testing.T) (app application, userDir, repository string) {
	t.Helper()
	userDir, repository = t.TempDir(), t.TempDir()
	environment := herdr.Environment{
		Context:   herdr.Context{WorkspaceID: "workspace", WorkspaceCWD: repository, FocusedPaneID: "author"},
		StateDir:  t.TempDir(),
		ConfigDir: userDir,
		Binary:    filepath.Join(t.TempDir(), "herdr-is-not-installed"),
	}
	resolve := func(invocation map[string]any) config.Resolution {
		return config.Resolve(config.Sources{User: environment.ConfigDir, Project: projectLayer(environment), Invocation: invocation})
	}
	resolution := resolve(nil)
	return application{
		environment: environment,
		values:      resolution.Values,
		client:      herdr.NewClient(environment.Binary),
		resolve:     resolve,
	}, userDir, repository
}

// capture runs a command with stdout redirected, since both subcommands are their own output.
func capture(t *testing.T, command func() error) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = write
	runErr := command()
	os.Stdout = stdout
	_ = write.Close()
	var out strings.Builder
	buffer := make([]byte, 4096)
	for {
		n, readErr := read.Read(buffer)
		out.Write(buffer[:n])
		if readErr != nil {
			break
		}
	}
	_ = read.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	return out.String()
}

func TestConfigNamesTheWinningLayerForEveryKeyAndTheCommandForEveryRound(t *testing.T) {
	app, userDir, repository := newApplication(t)
	if err := os.WriteFile(config.Path(userDir), []byte(`{"retries":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(repository, loop.PluginDir)
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(project), []byte(`{"max_iterations":4}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() error { return configCommand(app, nil) })
	for _, want := range []string{"max_iterations      4", "retries             0", "archive.keep", "default", "rounds", "profiles"} {
		if !strings.Contains(out, want) {
			t.Fatalf("config output is missing %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "max_iterations") && !strings.HasSuffix(line, config.LayerProject) {
			t.Fatalf("max_iterations was not attributed to the project layer: %q", line)
		}
		if strings.Contains(line, "  retries ") && !strings.HasSuffix(line, config.LayerUser) {
			t.Fatalf("retries was not attributed to the user layer: %q", line)
		}
	}
	// every round of the policy has to be named with the command that will run it
	for _, round := range []string{"1  built-in review prompt", "2  built-in review prompt", "3  built-in review prompt"} {
		if !strings.Contains(out, round) {
			t.Fatalf("config output is missing round %q:\n%s", round, out)
		}
	}
}

func TestInitProducesAProjectLayerThatChangesNoBehavior(t *testing.T) {
	app, userDir, repository := newApplication(t)
	if err := os.WriteFile(config.Path(userDir), []byte(`{"retries":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := app.resolve(nil)
	capture(t, func() error { return initCommand(app, nil) })
	project := filepath.Join(repository, loop.PluginDir)
	for _, name := range []string{"config.json", "config.example.md", filepath.Join(config.ProfileSubdir, "default.json")} {
		if _, err := os.Stat(filepath.Join(project, name)); err != nil {
			t.Fatalf("init did not write %s: %v", name, err)
		}
	}
	after := app.resolve(nil)
	if len(after.Warnings) != 0 {
		t.Fatalf("the loader rejected what init wrote: %#v", after.Warnings)
	}
	if !config.Same(after.Values, before.Values) {
		t.Fatalf("init changed behavior: %#v became %#v", before.Values, after.Values)
	}
	if len(after.Values.Rounds) != len(before.Values.Rounds) {
		t.Fatalf("init changed the round policy: %#v became %#v", before.Values.Rounds, after.Values.Rounds)
	}
	// the written config.json holds what the user overrides, never the resolved state
	data, err := os.ReadFile(config.Path(project))
	if err != nil {
		t.Fatal(err)
	}
	written := string(data)
	if !strings.Contains(written, `"retries": 0`) {
		t.Fatalf("init dropped the setting the user actually overrides:\n%s", written)
	}
	for _, frozen := range []string{"max_iterations", "archive", "timeouts"} {
		if strings.Contains(written, frozen) {
			t.Fatalf("init froze today's default %q:\n%s", frozen, written)
		}
	}
}

func TestInitCarriesTheUsersStructuredOverridesDown(t *testing.T) {
	app, userDir, repository := newApplication(t)
	overrides := `{"rounds":[{"level":"max"}],"review_command":{"codex":"/deep-review"}}`
	if err := os.WriteFile(config.Path(userDir), []byte(overrides), 0o600); err != nil {
		t.Fatal(err)
	}
	before := app.resolve(nil)
	capture(t, func() error { return initCommand(app, nil) })
	project := filepath.Join(repository, loop.PluginDir)
	data, err := os.ReadFile(config.Path(project))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"rounds", "max", "review_command", "/deep-review"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("init dropped the user's %q override:\n%s", want, data)
		}
	}
	// what the project reproduces on its own has to be the policy the run was already using
	after := config.Resolve(config.Sources{Project: project})
	if len(after.Values.Rounds) != len(before.Values.Rounds) || after.Values.Rounds[0] != before.Values.Rounds[0] {
		t.Fatalf("init changed the round policy: %#v became %#v", before.Values.Rounds, after.Values.Rounds)
	}
	if after.Values.ReviewCommand["codex"] != before.Values.ReviewCommand["codex"] {
		t.Fatalf("init changed the review command: %#v became %#v", before.Values.ReviewCommand, after.Values.ReviewCommand)
	}
}

func TestInitKeepsWhatTheProjectAlreadyCommitted(t *testing.T) {
	app, _, repository := newApplication(t)
	project := filepath.Join(repository, loop.PluginDir)
	if err := os.MkdirAll(filepath.Join(project, config.ProfileSubdir), 0o700); err != nil {
		t.Fatal(err)
	}
	existing := `{"rounds":[{"level":"max"}]}`
	if err := os.WriteFile(filepath.Join(project, config.ProfileSubdir, "default.json"), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() error { return initCommand(app, nil) })
	if !strings.Contains(out, "wrote") {
		t.Fatalf("init reported nothing written:\n%s", out)
	}
	data, err := os.ReadFile(filepath.Join(project, config.ProfileSubdir, "default.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != existing {
		t.Fatalf("init overwrote a committed profile: %s", data)
	}
}

func TestInitRefusesWithoutAWorkspace(t *testing.T) {
	app, _, _ := newApplication(t)
	app.environment.Context = herdr.Context{}
	err := initCommand(app, nil)
	if err == nil || loop.ExitCode(err) != loop.ExitTool {
		t.Fatalf("got %v (code %d), want a tool error", err, loop.ExitCode(err))
	}
}

func TestConfigAndInitRejectArguments(t *testing.T) {
	app, _, _ := newApplication(t)
	for _, command := range []func(application, []string) error{configCommand, initCommand} {
		if err := command(app, []string{"extra"}); !errors.Is(err, errUsage) {
			t.Fatalf("got %v, want a usage error", err)
		}
	}
}
