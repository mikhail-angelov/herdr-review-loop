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

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

type Client struct{ Binary string }

func NewClient(binary string) Client { return Client{Binary: binary} }

func (c Client) Call(ctx context.Context, args ...string) (json.RawMessage, error) {
	command := exec.CommandContext(ctx, c.Binary, args...)
	stdout, err := command.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var stderr string
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		stderr = string(exit.Stderr)
	}
	body := stdout
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte(stderr)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		if envelope.Error != nil {
			return nil, envelope.Error
		}
		if err == nil {
			return envelope.Result, nil
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

func (c Client) Text(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, c.Binary, args...)
	output, err := command.Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func timeoutArg(timeout time.Duration) string {
	if timeout < 0 {
		timeout = 0
	}
	return fmt.Sprintf("%d", timeout.Milliseconds())
}

func (c Client) AgentList(ctx context.Context) ([]Agent, error) {
	var value struct {
		Agents []Agent `json:"agents"`
	}
	raw, err := c.Call(ctx, "agent", "list")
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(raw, &value)
	return value.Agents, err
}
func (c Client) AgentGet(ctx context.Context, target string) (Agent, error) {
	var value struct {
		Agent Agent `json:"agent"`
	}
	raw, err := c.Call(ctx, "agent", "get", target)
	if err != nil {
		return Agent{}, err
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		return Agent{}, err
	}
	return value.Agent, nil
}
func (c Client) AgentWait(ctx context.Context, target string, timeout time.Duration) (Agent, error) {
	var value struct {
		Agent Agent `json:"agent"`
	}
	raw, err := c.Call(ctx, "agent", "wait", target, "--timeout", timeoutArg(timeout))
	if err != nil {
		return Agent{}, err
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		return Agent{}, err
	}
	return value.Agent, nil
}
func (c Client) AgentPrompt(ctx context.Context, target, prompt string, timeout time.Duration) (Agent, error) {
	var value struct {
		Agent Agent `json:"agent"`
	}
	raw, err := c.Call(ctx, "agent", "prompt", target, prompt, "--wait", "--timeout", timeoutArg(timeout))
	if err != nil {
		return Agent{}, err
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		return Agent{}, err
	}
	return value.Agent, nil
}
func (c Client) AgentFocus(ctx context.Context, target string) error {
	_, err := c.Call(ctx, "agent", "focus", target)
	return err
}
func (c Client) AgentSendKeys(ctx context.Context, target, keys string) error {
	_, err := c.Call(ctx, "agent", "send-keys", target, keys)
	return err
}
func (c Client) PaneSendText(ctx context.Context, pane, value string) error {
	_, err := c.Call(ctx, "pane", "send-text", pane, value)
	return err
}
func (c Client) PaneSendKeys(ctx context.Context, pane, keys string) error {
	_, err := c.Call(ctx, "pane", "send-keys", pane, keys)
	return err
}
func (c Client) PaneRead(ctx context.Context, pane string) (string, error) {
	return c.Text(ctx, "pane", "read", pane, "--source", "visible", "--format", "text")
}
func (c Client) WorkspaceReportMetadata(ctx context.Context, workspace, phase string, clear bool) error {
	args := []string{"workspace", "report-metadata", workspace, "--source", "plugin:herdr-review-loop"}
	if clear {
		args = append(args, "--clear-token", "review")
	} else {
		args = append(args, "--token", "review="+phase)
	}
	_, err := c.Call(ctx, args...)
	return err
}
func (c Client) NotificationShow(ctx context.Context, title, body string) error {
	_, err := c.Call(ctx, "notification", "show", title, "--body", body)
	return err
}

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

func (c Client) PluginPaneOpen(ctx context.Context, target, author string) (string, error) {
	raw, err := c.Call(ctx, "plugin", "pane", "open", "--plugin", "herdr-review-loop", "--entrypoint", "panel", "--placement", "split", "--direction", "right", "--target-pane", target, "--no-focus", "--env", "HERDR_REVIEW_LOOP_AUTHOR="+author)
	if err != nil {
		return "", err
	}
	var result struct {
		PaneID string `json:"pane_id"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	return result.PaneID, nil
}
func (c Client) PaneLayout(ctx context.Context, pane string) (PaneLayout, error) {
	raw, err := c.Call(ctx, "pane", "layout", "--pane", pane)
	if err != nil {
		return PaneLayout{}, err
	}
	var result struct {
		Layout PaneLayout `json:"layout"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return PaneLayout{}, err
	}
	return result.Layout, nil
}
func (c Client) PaneResize(ctx context.Context, pane, direction string, amount int) error {
	_, err := c.Call(ctx, "pane", "resize", "--pane", pane, "--direction", direction, "--amount", fmt.Sprintf("%d", amount))
	return err
}
