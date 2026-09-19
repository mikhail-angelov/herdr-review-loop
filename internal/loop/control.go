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

// The two control files. Each has exactly one writer — the run publishes what it is waiting for,
// the panel publishes what the user asked of it — so a request and an answer can never overwrite
// each other, and neither has to lock against the other.
const (
	pendingFile = "pending.json"
	controlFile = "control.json"
)

// The decisions a run stops for. The kind is what the panel keys its choices off, and it is part
// of a request's identity, so an answer to one boundary is never mistaken for an answer to the next.
const (
	// PendingOneShot is the single choice a one-shot review ends with.
	PendingOneShot = "one-shot"
	// PendingGateReview stops between the review and the author's turn, so the findings can be
	// read before anything is changed.
	PendingGateReview = "gate-review"
	// PendingGateRound stops after a round, so what the author changed can be read before the
	// next review judges it.
	PendingGateRound = "gate-round"
)

// The answers a panel can give. Continue and Step differ only in what happens at the next
// boundary: Step stops there again whatever the configuration says.
const (
	ChoiceContinue = "continue"
	ChoiceStep     = "step"
	ChoiceStop     = "stop"
	ChoiceInstruct = "instruct"
	ChoiceApply    = "apply"
	ChoiceCancel   = "cancel"
)

// Choice is one answer, naming the request it answers. A run ignores an answer addressed to a
// request it is no longer waiting on, which is what makes a stale control file harmless.
type Choice struct {
	Request string `json:"request"`
	Action  string `json:"action"`
	Text    string `json:"text,omitempty"`
}

// Control is what the panel asks of the running loop: a pause honored at the next boundary, and
// the answer to whatever the loop is currently waiting for. The panel owns this file completely.
type Control struct {
	Workspace  string `json:"workspace"`
	Repository string `json:"repository"`
	Pause      bool   `json:"pause"`
	Choice     Choice `json:"choice,omitzero"`
}

// Pending is the decision a run is blocked on, published for whichever panel is watching.
type Pending struct {
	ID         string `json:"id"`
	Workspace  string `json:"workspace"`
	Repository string `json:"repository"`
	Run        string `json:"run"`
	Round      int    `json:"round"`
	Kind       string `json:"kind"`
	Summary    string `json:"summary"`
}

func pendingPath(stateDir string) string { return filepath.Join(stateDir, pendingFile) }
func controlPath(stateDir string) string { return filepath.Join(stateDir, controlFile) }

// requestID identifies one boundary of one run. Round and kind are enough: a run stops at each
// boundary once, and a retry of a phase happens before the boundary is reached.
func requestID(run string, round int, kind string) string {
	return fmt.Sprintf("%s#%d#%s", run, round, kind)
}

// publishPending announces what the run is waiting for. The state directory already exists by the
// time a run reaches a boundary, but a one-shot run may publish before anything else has written.
func publishPending(stateDir string, pending Pending) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	return writeJSON(pendingPath(stateDir), pending)
}

func clearPending(stateDir string) { _ = os.Remove(pendingPath(stateDir)) }

// PendingRequest reports what this workspace's loop is waiting for, if anything. The repository is
// checked too, because one workspace's state directory is shared by every repository opened in it.
func PendingRequest(stateDir, workspace, repository string) (Pending, bool) {
	data, err := os.ReadFile(pendingPath(stateDir))
	if err != nil {
		return Pending{}, false
	}
	var pending Pending
	if json.Unmarshal(data, &pending) != nil {
		return Pending{}, false
	}
	if pending.Workspace != workspace || pending.Repository != repository {
		return Pending{}, false
	}
	return pending, true
}

// readControl returns what the panel last asked for. A missing or unreadable file is no request
// rather than an error: the panel may never have written one.
func readControl(stateDir, workspace, repository string) Control {
	data, err := os.ReadFile(controlPath(stateDir))
	if err != nil {
		return Control{}
	}
	var control Control
	if json.Unmarshal(data, &control) != nil {
		return Control{}
	}
	if control.Workspace != workspace || control.Repository != repository {
		return Control{}
	}
	return control
}

// Paused reports whether the panel has asked the loop to stop at its next boundary.
func Paused(stateDir, workspace, repository string) bool {
	return readControl(stateDir, workspace, repository).Pause
}

// SetPause asks the running loop to stop at its next boundary, or withdraws that request. The
// answer to a pending request is left in place: withdrawing a pause must not withdraw an answer
// the loop has not read yet.
func SetPause(stateDir, workspace, repository string, pause bool) error {
	control := readControl(stateDir, workspace, repository)
	control.Workspace, control.Repository, control.Pause = workspace, repository, pause
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	return writeJSON(controlPath(stateDir), control)
}

// Answer records the user's choice and clears the pause that produced the boundary, so a loop told
// to continue does not stop again on the pause request it already honored.
func Answer(stateDir string, pending Pending, action, text string) error {
	if !answerable(pending.Kind, action) {
		return fmt.Errorf("%s cannot be answered with %q", pending.Kind, action)
	}
	text = strings.TrimSpace(text)
	if action == ChoiceInstruct && text == "" {
		return errors.New("an instruction cannot be empty")
	}
	control := readControl(stateDir, pending.Workspace, pending.Repository)
	control.Workspace, control.Repository = pending.Workspace, pending.Repository
	control.Pause = false
	control.Choice = Choice{Request: pending.ID, Action: action, Text: text}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	return writeJSON(controlPath(stateDir), control)
}

// answerable reports whether an action means anything at this kind of boundary. The panel offers
// only the right keys, but the control file is a file, and a run must not act on nonsense.
func answerable(kind, action string) bool {
	switch kind {
	case PendingOneShot:
		return action == ChoiceApply || action == ChoiceCancel || action == ChoiceInstruct
	case PendingGateReview, PendingGateRound:
		return action == ChoiceContinue || action == ChoiceStep || action == ChoiceStop || action == ChoiceInstruct
	default:
		return false
	}
}

// awaitChoice blocks until the panel answers this request. Polling is what a file handoff allows,
// and the interval is the one a keypress should feel immediate at.
func awaitChoice(ctx context.Context, stateDir string, pending Pending) (Choice, error) {
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		control := readControl(stateDir, pending.Workspace, pending.Repository)
		if control.Choice.Request == pending.ID && answerable(pending.Kind, control.Choice.Action) {
			return control.Choice, nil
		}
		select {
		case <-ctx.Done():
			return Choice{}, fmt.Errorf("waiting for a panel choice: %w", ctx.Err())
		case <-tick.C:
		}
	}
}

// ClearControl removes both control files. It runs when a run ends, so the next one starts without
// an answer or a pause left over from the last.
func ClearControl(stateDir string) {
	_ = os.Remove(pendingPath(stateDir))
	_ = os.Remove(controlPath(stateDir))
}
