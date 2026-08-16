package herdr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Context is the slice of Herdr's plugin context this tool needs, decoded from
// HERDR_PLUGIN_CONTEXT_JSON.
type Context struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceCWD   string `json:"workspace_cwd"`
	FocusedPaneID  string `json:"focused_pane_id"`
	FocusedPaneCWD string `json:"focused_pane_cwd"`
}

// Environment holds everything the host tells the plugin about where to run: the context, the
// directories it owns, and the CLI to talk back through.
type Environment struct {
	Context   Context
	StateDir  string
	ConfigDir string
	Binary    string
	PaneID    string
}

// LoadEnvironment reads the plugin environment Herdr exports. The working directory is the fallback
// for the state and config directories so the tool still runs when launched by hand.
func LoadEnvironment() (Environment, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return Environment{}, fmt.Errorf("failed to resolve working directory: %w", err)
	}
	env := Environment{StateDir: envOr("HERDR_PLUGIN_STATE_DIR", workingDirectory), ConfigDir: envOr("HERDR_PLUGIN_CONFIG_DIR", workingDirectory), Binary: envOr("HERDR_BIN_PATH", "herdr"), PaneID: os.Getenv("HERDR_PANE_ID")}
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &env.Context); err != nil {
			return Environment{}, fmt.Errorf("invalid HERDR_PLUGIN_CONTEXT_JSON: %w", err)
		}
	}
	if author := os.Getenv("HERDR_REVIEW_LOOP_AUTHOR"); author != "" {
		env.Context.FocusedPaneID = author
	}
	return env, nil
}

// Repository is the git checkout the loop operates on: the workspace directory when Herdr provides
// one, otherwise the focused pane's directory.
func (e Environment) Repository() (string, error) {
	if e.Context.WorkspaceCWD != "" {
		return filepath.Clean(e.Context.WorkspaceCWD), nil
	}
	if e.Context.FocusedPaneCWD != "" {
		return filepath.Clean(e.Context.FocusedPaneCWD), nil
	}
	return "", errors.New("no workspace directory in Herdr context")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
