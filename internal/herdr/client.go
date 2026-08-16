package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Error is the coded failure Herdr reports inside a JSON envelope. It is kept as a distinct type so
// callers can branch on Code with errors.As instead of matching on message text.
type Error struct {
	Code    string
	Message string
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

// Client runs the Herdr CLI. Every call shells out; there is no long-lived connection to keep alive.
type Client struct{ Binary string }

// NewClient makes a client invoking the Herdr binary at the given path.
func NewClient(binary string) Client { return Client{Binary: binary} }

// command builds a Herdr invocation. Binary comes from HERDR_BIN_PATH, which the host that launched
// this plugin controls, and every argument is assembled inside this package — no shell is involved.
func (c Client) command(ctx context.Context, args []string) *exec.Cmd {
	return exec.CommandContext(ctx, c.Binary, args...) //nolint:gosec // binary and args are ours, not user input
}

// Call runs a Herdr command and returns the result member of its JSON envelope. A canceled context
// wins over whatever the process wrote, so a killed child is never mistaken for a failed command.
func (c Client) Call(ctx context.Context, args ...string) (json.RawMessage, error) {
	stdout, err := c.command(ctx, args).Output()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("herdr %s: %w", strings.Join(args, " "), ctx.Err())
	}
	var stderr string
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		stderr = string(exit.Stderr)
	}
	// herdr writes the envelope to stdout on success and to stderr on failure, but not
	// consistently, so both streams are tried before the exit code is trusted
	if result, envelopeErr, ok := envelope(stdout); ok {
		if envelopeErr != nil {
			return nil, envelopeErr
		}
		if err == nil {
			return result, nil
		}
	}
	if result, envelopeErr, ok := envelope([]byte(stderr)); ok {
		if envelopeErr != nil {
			return nil, envelopeErr
		}
		if err == nil {
			return result, nil
		}
	}
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return nil, fmt.Errorf("herdr %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr))
	}
	return nil, fmt.Errorf("herdr %s returned invalid JSON", strings.Join(args, " "))
}

func envelope(body []byte) (json.RawMessage, *Error, bool) {
	if strings.TrimSpace(string(body)) == "" {
		return nil, nil, false
	}
	var value struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if json.Unmarshal(body, &value) != nil {
		return nil, nil, false
	}
	return value.Result, value.Error, true
}

// Text runs a Herdr command and returns its stdout verbatim, for the commands that emit raw text
// rather than a JSON envelope.
func (c Client) Text(ctx context.Context, args ...string) (string, error) {
	output, err := c.command(ctx, args).Output()
	if ctx.Err() != nil {
		return "", fmt.Errorf("herdr %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.TrimSpace(string(exit.Stderr)) != "" {
			return "", fmt.Errorf("herdr %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("herdr %s: %w", strings.Join(args, " "), err)
	}
	return string(output), nil
}

// void runs a command whose success carries no payload: empty output is a valid result, so only an
// error envelope or a non-zero exit is a failure.
func (c Client) void(ctx context.Context, args ...string) error {
	stdout, err := c.command(ctx, args).Output()
	if ctx.Err() != nil {
		return fmt.Errorf("herdr %s: %w", strings.Join(args, " "), ctx.Err())
	}
	if _, envelopeErr, ok := envelope(stdout); ok && envelopeErr != nil {
		return envelopeErr
	}
	var stderr string
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		stderr = string(exit.Stderr)
	}
	if _, envelopeErr, ok := envelope([]byte(stderr)); ok && envelopeErr != nil {
		return envelopeErr
	}
	if err != nil {
		if stderr == "" {
			stderr = err.Error()
		}
		return fmt.Errorf("herdr %s: %s", strings.Join(args, " "), strings.TrimSpace(stderr))
	}
	return nil
}

func timeoutArg(timeout time.Duration) string {
	if timeout < 0 {
		timeout = 0
	}
	return fmt.Sprintf("%d", timeout.Milliseconds())
}

// decode unmarshals an envelope result, naming the command so a schema change reads as a Herdr
// problem rather than as a generic JSON error.
func decode[T any](raw json.RawMessage, command string) (T, error) {
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("herdr %s returned unreadable JSON: %w", command, err)
	}
	return value, nil
}

// AgentList returns every agent Herdr knows about, across all workspaces.
func (c Client) AgentList(ctx context.Context) ([]Agent, error) {
	raw, err := c.Call(ctx, "agent", "list")
	if err != nil {
		return nil, err
	}
	value, err := decode[struct {
		Agents []Agent `json:"agents"`
	}](raw, "agent list")
	return value.Agents, err
}

// AgentGet returns a single agent by pane id or name.
func (c Client) AgentGet(ctx context.Context, target string) (Agent, error) {
	raw, err := c.Call(ctx, "agent", "get", target)
	if err != nil {
		return Agent{}, err
	}
	value, err := decode[struct {
		Agent Agent `json:"agent"`
	}](raw, "agent get")
	return value.Agent, err
}

// AgentWait blocks until the agent leaves its busy state or the timeout expires.
func (c Client) AgentWait(ctx context.Context, target string, timeout time.Duration) (Agent, error) {
	raw, err := c.Call(ctx, "agent", "wait", target, "--timeout", timeoutArg(timeout))
	if err != nil {
		return Agent{}, err
	}
	value, err := decode[struct {
		Agent Agent `json:"agent"`
	}](raw, "agent wait")
	return value.Agent, err
}

// AgentPrompt sends a prompt to an agent and waits for it to finish answering.
func (c Client) AgentPrompt(ctx context.Context, target, prompt string, timeout time.Duration) (Agent, error) {
	raw, err := c.Call(ctx, "agent", "prompt", target, prompt, "--wait", "--timeout", timeoutArg(timeout))
	if err != nil {
		return Agent{}, err
	}
	value, err := decode[struct {
		Agent Agent `json:"agent"`
	}](raw, "agent prompt")
	return value.Agent, err
}

// AgentFocus moves terminal focus to the agent's pane.
func (c Client) AgentFocus(ctx context.Context, target string) error {
	_, err := c.Call(ctx, "agent", "focus", target)
	return err
}

// AgentSendKeys delivers a key sequence to the agent, bypassing its prompt input.
func (c Client) AgentSendKeys(ctx context.Context, target, keys string) error {
	return c.void(ctx, "agent", "send-keys", target, keys)
}

// PaneSendText types text into a pane without submitting it.
func (c Client) PaneSendText(ctx context.Context, pane, value string) error {
	return c.void(ctx, "pane", "send-text", pane, value)
}

// PaneSendKeys delivers a key sequence to a pane.
func (c Client) PaneSendKeys(ctx context.Context, pane, keys string) error {
	return c.void(ctx, "pane", "send-keys", pane, keys)
}

// PaneRead returns the visible contents of a pane as plain text.
func (c Client) PaneRead(ctx context.Context, pane string) (string, error) {
	return c.Text(ctx, "pane", "read", pane, "--source", "visible", "--format", "text")
}

// WorkspaceReportMetadata publishes the loop's phase as a workspace token, or clears it.
func (c Client) WorkspaceReportMetadata(ctx context.Context, workspace, phase string, clearToken bool) error {
	args := []string{"workspace", "report-metadata", workspace, "--source", "plugin:herdr-review-loop"}
	if clearToken {
		args = append(args, "--clear-token", "review")
	} else {
		args = append(args, "--token", "review="+phase)
	}
	return c.void(ctx, args...)
}

// NotificationShow raises a desktop notification through Herdr.
func (c Client) NotificationShow(ctx context.Context, title, body string) error {
	_, err := c.Call(ctx, "notification", "show", title, "--body", body)
	return err
}

// PaneLayout describes the geometry of a window, used to size the plugin pane relative to its area.
type PaneLayout struct {
	Area struct {
		Width int `json:"width"`
	} `json:"area"`
	Panes []struct {
		PaneID string `json:"pane_id"`
		Rect   struct {
			Width int `json:"width"`
		} `json:"rect"`
	} `json:"panes"`
}

// PluginPaneOpen splits a pane and starts the panel entrypoint in it, returning the new pane id.
func (c Client) PluginPaneOpen(ctx context.Context, target, author string) (string, error) {
	raw, err := c.Call(ctx, "plugin", "pane", "open", "--plugin", "herdr-review-loop", "--entrypoint", "panel",
		"--placement", "split", "--direction", "right", "--target-pane", target, "--no-focus",
		"--env", "HERDR_REVIEW_LOOP_AUTHOR="+author)
	if err != nil {
		return "", err
	}
	result, err := decode[struct {
		PluginPane struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"plugin_pane"`
	}](raw, "plugin pane open")
	if err != nil {
		return "", err
	}
	if result.PluginPane.Pane.PaneID == "" {
		return "", errors.New("plugin pane open returned no pane id")
	}
	return result.PluginPane.Pane.PaneID, nil
}

// PaneLayout returns the geometry of the window holding the given pane.
func (c Client) PaneLayout(ctx context.Context, pane string) (PaneLayout, error) {
	raw, err := c.Call(ctx, "pane", "layout", "--pane", pane)
	if err != nil {
		return PaneLayout{}, err
	}
	result, err := decode[struct {
		Layout PaneLayout `json:"layout"`
	}](raw, "pane layout")
	return result.Layout, err
}

// PaneResize grows or shrinks a pane by a fraction of its window.
func (c Client) PaneResize(ctx context.Context, pane, direction string, amount float64) error {
	_, err := c.Call(ctx, "pane", "resize", "--pane", pane, "--direction", direction, "--amount", fmt.Sprintf("%.3f", amount))
	return err
}

// PluginPaneFocus moves terminal focus to a plugin pane.
func (c Client) PluginPaneFocus(ctx context.Context, pane string) error {
	_, err := c.Call(ctx, "plugin", "pane", "focus", pane)
	return err
}
