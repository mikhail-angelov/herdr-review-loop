package loop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

type AgentClient interface {
	AgentGet(context.Context, string) (herdr.Agent, error)
	AgentWait(context.Context, string, time.Duration) (herdr.Agent, error)
	AgentPrompt(context.Context, string, string, time.Duration) (herdr.Agent, error)
	AgentFocus(context.Context, string) error
	AgentSendKeys(context.Context, string, string) error
	PaneSendText(context.Context, string, string) error
	PaneSendKeys(context.Context, string, string) error
	PaneRead(context.Context, string) (string, error)
}

func remaining(ctx context.Context) (time.Duration, error) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 24 * time.Hour, nil
	}
	left := time.Until(deadline)
	if left <= 0 {
		return 0, context.DeadlineExceeded
	}
	return left, nil
}
func Settle(ctx context.Context, client AgentClient, agent herdr.Agent) (herdr.Agent, error) {
	current, err := client.AgentGet(ctx, herdr.Target(agent))
	if err != nil {
		return herdr.Agent{}, err
	}
	if current.Status == "blocked" {
		_ = client.AgentFocus(ctx, herdr.Target(current))
		return current, fmt.Errorf("%s is blocked; answer it, then run the loop again", herdr.Describe(current))
	}
	if current.Status != "working" {
		return current, nil
	}
	budget, err := remaining(ctx)
	if err != nil {
		return current, err
	}
	current, err = client.AgentWait(ctx, herdr.Target(current), budget)
	if err != nil {
		return current, err
	}
	if current.Status == "blocked" {
		_ = client.AgentFocus(ctx, herdr.Target(current))
		return current, fmt.Errorf("%s is blocked; answer it, then run the loop again", herdr.Describe(current))
	}
	return current, nil
}

func SubmitAndWait(ctx context.Context, client AgentClient, agent herdr.Agent, prompt string) (herdr.Agent, error) {
	before, err := client.AgentGet(ctx, herdr.Target(agent))
	if err != nil {
		return herdr.Agent{}, err
	}
	budget, err := remaining(ctx)
	if err != nil {
		return herdr.Agent{}, err
	}
	result, err := client.AgentPrompt(ctx, herdr.Target(agent), prompt, budget)
	if err == nil {
		if result.Status == "blocked" {
			_ = client.AgentFocus(ctx, herdr.Target(result))
			return result, fmt.Errorf("%s is blocked; answer it, then run the loop again", herdr.Describe(result))
		}
		return result, nil
	}
	var coded *herdr.Error
	if !errors.As(err, &coded) || coded.Code != "agent_prompt_stalled" {
		return herdr.Agent{}, err
	}
	if advanced(ctx, client, agent, before.StateChangeSeq, 5*time.Second) {
		budget, err = remaining(ctx)
		if err != nil {
			return herdr.Agent{}, err
		}
		return waitAfterPrompt(ctx, client, agent, budget)
	}
	for range 3 {
		if err := client.AgentSendKeys(ctx, herdr.Target(agent), "enter"); err != nil {
			return herdr.Agent{}, err
		}
		if advanced(ctx, client, agent, before.StateChangeSeq, 4*time.Second) {
			budget, err = remaining(ctx)
			if err != nil {
				return herdr.Agent{}, err
			}
			return waitAfterPrompt(ctx, client, agent, budget)
		}
	}
	return herdr.Agent{}, fmt.Errorf("the prompt did not start a turn")
}

func waitAfterPrompt(ctx context.Context, client AgentClient, agent herdr.Agent, budget time.Duration) (herdr.Agent, error) {
	result, err := client.AgentWait(ctx, herdr.Target(agent), budget)
	if err == nil && result.Status == "blocked" {
		_ = client.AgentFocus(ctx, herdr.Target(result))
		return result, fmt.Errorf("%s is blocked; answer it, then run the loop again", herdr.Describe(result))
	}
	return result, err
}
func advanced(ctx context.Context, client AgentClient, agent herdr.Agent, sequence int64, limit time.Duration) bool {
	timer := time.NewTimer(limit)
	defer timer.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		current, err := client.AgentGet(ctx, herdr.Target(agent))
		if err == nil && current.StateChangeSeq > sequence {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-tick.C:
		}
	}
}

func resetCommand(agent herdr.Agent, configured string) string {
	command := map[string]string{"claude": "/clear", "gemini": "/clear", "codex": "/new", "opencode": "/new"}[agent.Kind]
	if command != "" {
		return command
	}
	return configured
}

func ValidateResetCommand(agent herdr.Agent, configured string) error {
	if resetCommand(agent, configured) == "" {
		return fmt.Errorf("no reset command for %s", herdr.Describe(agent))
	}
	return nil
}

func ResetSession(ctx context.Context, client AgentClient, agent herdr.Agent, configured string, log func(string)) error {
	command := resetCommand(agent, configured)
	if command == "" {
		if log != nil {
			log("no reset command for " + agent.Kind)
		}
		return ValidateResetCommand(agent, configured)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := client.PaneSendText(ctx, agent.PaneID, command); err != nil {
			return err
		}
		if !pause(ctx, 400*time.Millisecond) {
			return ctx.Err()
		}
		visible, err := client.PaneRead(ctx, agent.PaneID)
		if err != nil {
			return err
		}
		if bottomContains(visible, command) {
			if err := client.PaneSendKeys(ctx, agent.PaneID, "enter"); err != nil {
				return err
			}
			pause(ctx, 1500*time.Millisecond)
			return ctx.Err()
		}
		if err := client.PaneSendKeys(ctx, agent.PaneID, "esc"); err != nil {
			return err
		}
		if !pause(ctx, 400*time.Millisecond) {
			return ctx.Err()
		}
	}
	if log != nil {
		log("reset command kept being dropped")
	}
	return fmt.Errorf("could not reset %s", herdr.Describe(agent))
}
func pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func bottomContains(visible, command string) bool {
	lines := strings.Split(visible, "\n")
	nonEmpty := make([]string, 0, 6)
	for index := len(lines) - 1; index >= 0 && len(nonEmpty) < 6; index-- {
		if strings.TrimSpace(lines[index]) != "" {
			nonEmpty = append(nonEmpty, lines[index])
		}
	}
	for _, line := range nonEmpty {
		if strings.Contains(line, command) {
			return true
		}
	}
	return false
}
