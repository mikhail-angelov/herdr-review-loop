package herdr

import (
	"os"
	"testing"
)

func TestLoadEnvironmentUsesDocumentedFallbacks(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "")
	t.Setenv("HERDR_PLUGIN_STATE_DIR", "")
	t.Setenv("HERDR_PLUGIN_CONFIG_DIR", "")
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("HERDR_PANE_ID", "pane")
	t.Setenv("HERDR_REVIEW_LOOP_AUTHOR", "author")
	environment, err := LoadEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if environment.StateDir != workingDirectory || environment.ConfigDir != workingDirectory || environment.Binary != "herdr" || environment.Context.FocusedPaneID != "author" || environment.PaneID != "pane" {
		t.Fatalf("unexpected environment: %#v", environment)
	}
}

func TestLoadEnvironmentRejectsMalformedContext(t *testing.T) {
	t.Setenv("HERDR_PLUGIN_CONTEXT_JSON", "{")
	if _, err := LoadEnvironment(); err == nil {
		t.Fatal("accepted malformed context")
	}
}
