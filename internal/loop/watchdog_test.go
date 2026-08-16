package loop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

// silentClient never produces output: every observation returns the same thing, which is exactly
// what an agent that has gone quiet looks like from outside.
type silentClient struct {
	runClient
	prompts chan struct{}
}

func (c *silentClient) AgentPrompt(ctx context.Context, target, _ string, _ time.Duration) (herdr.Agent, error) {
	select {
	case c.prompts <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return herdr.Agent{}, ctx.Err()
}

func newSilentClient() *silentClient {
	return &silentClient{runClient: runClient{agents: pairedAgents()}, prompts: make(chan struct{}, 8)}
}

func TestWatchStallFiresOnSilenceAndNotOnProgress(t *testing.T) {
	client := newSilentClient()
	agent := pairedAgents()[1]
	stalled, err := WatchStall(context.Background(), client, agent, 200*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !stalled || !errors.Is(err, context.Canceled) {
		t.Fatalf("stalled=%v err=%v, want a stall that canceled the work", stalled, err)
	}
	// work that finishes on its own is never a stall, however long the budget
	stalled, err = WatchStall(context.Background(), client, agent, time.Millisecond, func(context.Context) error { return nil })
	if stalled || err != nil {
		t.Fatalf("stalled=%v err=%v, want completed work", stalled, err)
	}
}

func TestWatchStallIsOffWhenTheBudgetIsZero(t *testing.T) {
	client := newSilentClient()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stalled, err := WatchStall(ctx, client, pairedAgents()[1], 0, func(watched context.Context) error {
		<-watched.Done()
		return watched.Err()
	})
	if stalled || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled=%v err=%v, want the phase budget to be the only limit", stalled, err)
	}
}

// TestASilentAgentIsCaughtByTheStallBudgetNotThePhaseBudget is T8.1's criterion: the phase budget
// here is half an hour, and the run has to end in a fraction of a second all the same.
func TestASilentAgentIsCaughtByTheStallBudgetNotThePhaseBudget(t *testing.T) {
	client := newSilentClient()
	values := quickValues()
	values.Retries = 1
	values.Timeouts.Stall = 150 * time.Millisecond
	run, _, state := newRun(t, client, values)
	err := run.Execute(context.Background(), false)
	if ExitCode(err) != ExitAgent {
		t.Fatalf("got %v (code %d), want a terminal agent failure", err, ExitCode(err))
	}
	// retries + 1 attempts were made, each of them stalled rather than waited out
	if got := len(client.prompts); got != values.Retries+1 {
		t.Fatalf("the reviewer was prompted %d time(s), want %d", got, values.Retries+1)
	}
	events := ReadEvents(ArchiveDir(state, ListRuns(state)[0].ID))
	stalls, retries := 0, 0
	for _, event := range events {
		switch event.Event {
		case EventStall:
			stalls++
		case EventRetry:
			retries++
		}
	}
	if stalls != values.Retries+1 || retries != values.Retries {
		t.Fatalf("events recorded %d stall(s) and %d retry/retries: %#v", stalls, retries, events)
	}
	if !strings.Contains(readLog(t, state), "no output from") {
		t.Fatal("the stall was not named in the log")
	}
}
