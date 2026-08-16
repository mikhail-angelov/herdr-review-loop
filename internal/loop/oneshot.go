package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const oneShotFile = "one-shot.json"

// One-shot action values accepted from the panel.
const (
	OneShotApply  = "apply"
	OneShotCancel = "cancel"
	OneShotCustom = "custom"
)

// OneShotChoice is the instruction selected in the panel after a one-shot review.
type OneShotChoice struct {
	Action string `json:"action"`
	Text   string `json:"text,omitempty"`
}

// OneShotPending is the durable handoff between the detached loop and the panel.
type OneShotPending struct {
	Workspace  string        `json:"workspace"`
	Repository string        `json:"repository"`
	Run        string        `json:"run"`
	Round      int           `json:"round"`
	Choice     OneShotChoice `json:"choice,omitempty"`
}

func oneShotPath(stateDir string) string { return filepath.Join(stateDir, oneShotFile) }

// CreateOneShotPending publishes a choice request after the author has seen the review.
func CreateOneShotPending(stateDir string, pending OneShotPending) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	return writeJSON(oneShotPath(stateDir), pending)
}

// PendingOneShot reports whether this panel's workspace has a review awaiting a choice.
func PendingOneShot(stateDir, workspace, repository string) (OneShotPending, bool) {
	data, err := os.ReadFile(oneShotPath(stateDir))
	if err != nil {
		return OneShotPending{}, false
	}
	var pending OneShotPending
	if json.Unmarshal(data, &pending) != nil || pending.Workspace != workspace || pending.Repository != repository || pending.Choice.Action != "" {
		return OneShotPending{}, false
	}
	return pending, true
}

// ChooseOneShot records the user's choice once. The waiting run consumes it and clears the file.
func ChooseOneShot(stateDir, workspace, repository, action, text string) error {
	pending, ok := PendingOneShot(stateDir, workspace, repository)
	if !ok {
		return errors.New("no one-shot review is awaiting a choice")
	}
	text = strings.TrimSpace(text)
	if action != OneShotApply && action != OneShotCancel && action != OneShotCustom {
		return fmt.Errorf("unknown one-shot action %q", action)
	}
	if action == OneShotCustom && text == "" {
		return errors.New("custom instruction cannot be empty")
	}
	pending.Choice = OneShotChoice{Action: action, Text: text}
	return writeJSON(oneShotPath(stateDir), pending)
}

func waitOneShotChoice(ctx context.Context, stateDir, workspace, repository string) (OneShotChoice, error) {
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		data, err := os.ReadFile(oneShotPath(stateDir))
		if err == nil {
			var pending OneShotPending
			if json.Unmarshal(data, &pending) == nil && pending.Workspace == workspace && pending.Repository == repository && pending.Choice.Action != "" {
				return pending.Choice, nil
			}
		}
		select {
		case <-ctx.Done():
			return OneShotChoice{}, fmt.Errorf("waiting for one-shot choice: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

func clearOneShot(stateDir string) { _ = os.Remove(oneShotPath(stateDir)) }
