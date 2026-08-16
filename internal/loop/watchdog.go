package loop

import (
	"context"
	"strconv"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

// stallPollFloor and stallPollCeiling bound how often the watchdog looks. A quarter of the budget
// catches a stall promptly without turning a thirty-minute phase into thousands of CLI calls.
const (
	stallPollFloor   = 250 * time.Millisecond
	stallPollCeiling = 10 * time.Second
)

// stallStopTimeout bounds the join, so a watcher wedged inside a CLI call cannot hold a phase open.
const stallStopTimeout = 5 * time.Second

// WatchStall runs work under a context that is canceled once the agent has produced no output for
// the stall budget, and reports whether that is what ended it. A stall is not a timeout: an agent
// that goes silent in minute two should cost the stall budget, not the whole phase.
//
// Progress is anything observable from outside the agent — its state sequence advancing, or its
// pane redrawing. Neither is a guarantee of useful work, but their absence is a reliable sign of
// none, which is the only claim the budget rests on.
func WatchStall(ctx context.Context, client AgentClient, agent herdr.Agent, stall time.Duration, work func(context.Context) error) (stalled bool, err error) {
	if stall <= 0 {
		return false, work(ctx)
	}
	watched, cancel := context.WithCancel(ctx)
	defer cancel()
	fired := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if watchForStall(watched, client, agent, stall) {
			close(fired)
			cancel()
		}
	}()
	err = work(watched)
	cancel()
	select {
	case <-done:
	case <-time.After(stallStopTimeout):
	}
	select {
	case <-fired:
		return true, err
	default:
		return false, err
	}
}

// watchForStall polls until the agent has gone quiet for the budget, or until the context ends.
func watchForStall(ctx context.Context, client AgentClient, agent herdr.Agent, stall time.Duration) bool {
	interval := min(max(stall/4, stallPollFloor), stallPollCeiling)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last, seen := time.Now(), observe(ctx, client, agent)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
		current := observe(ctx, client, agent)
		if current != seen {
			last, seen = time.Now(), current
			continue
		}
		if time.Since(last) >= stall {
			return true
		}
	}
}

// observe is everything the loop can see of an agent's progress from outside it. A call that fails
// returns what it has: an unreadable pane is not evidence of progress, and the phase budget still
// bounds a CLI that has stopped answering altogether.
func observe(ctx context.Context, client AgentClient, agent herdr.Agent) string {
	state := ""
	if current, err := client.AgentGet(ctx, herdr.Target(agent)); err == nil {
		state = current.Status + ":" + strconv.FormatInt(current.StateChangeSeq, 10)
	}
	if visible, err := client.PaneRead(ctx, agent.PaneID); err == nil {
		state += "\x00" + visible
	}
	return state
}
