package herdr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Context struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceCWD   string `json:"workspace_cwd"`
	FocusedPaneID  string `json:"focused_pane_id"`
	FocusedPaneCWD string `json:"focused_pane_cwd"`
}

type Environment struct {
	Context   Context
	StateDir  string
	ConfigDir string
	Binary    string
	PaneID    string
}

func LoadEnvironment() (Environment, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return Environment{}, err
	}
	env := Environment{StateDir: envOr("HERDR_PLUGIN_STATE_DIR", workingDirectory), ConfigDir: envOr("HERDR_PLUGIN_CONFIG_DIR", workingDirectory), Binary: envOr("HERDR_BIN_PATH", "herdr"), PaneID: os.Getenv("HERDR_PANE_ID")}
	raw := os.Getenv("HERDR_PLUGIN_CONTEXT_JSON")
	if raw == "" {
		return env, nil
	}
	if err := json.Unmarshal([]byte(raw), &env.Context); err != nil {
		return Environment{}, fmt.Errorf("invalid HERDR_PLUGIN_CONTEXT_JSON: %w", err)
	}
	if author := os.Getenv("HERDR_REVIEW_LOOP_AUTHOR"); author != "" {
		env.Context.FocusedPaneID = author
	}
	return env, nil
}

func (e Environment) Repository() (string, error) {
	if e.Context.WorkspaceCWD != "" {
		return filepath.Clean(e.Context.WorkspaceCWD), nil
	}
	if e.Context.FocusedPaneCWD != "" {
		return filepath.Clean(e.Context.FocusedPaneCWD), nil
	}
	return "", fmt.Errorf("no workspace directory in Herdr context")
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
