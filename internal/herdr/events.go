package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Subscription is one class of event to receive. The pane events are per pane: Herdr refuses a
// subscription to them without the pane it is about.
type Subscription struct {
	Type   string `json:"type"`
	PaneID string `json:"pane_id,omitempty"`
}

// The subscriptions this plugin uses. A metadata update is how a run announces its phase, so it is
// the panel's push channel from the loop; a status change is the agent itself going quiet or busy.
const (
	SubscriptionWorkspaceMetadata = "workspace.metadata_updated"
	SubscriptionAgentStatus       = "pane.agent_status_changed"
)

// Notice is an event as the panel needs it: what changed, on which pane, and the agent status the
// change left behind. Nothing else is decoded — the panel re-reads its own files, and an event is
// the signal to do that now rather than on the next tick.
type Notice struct {
	Kind   string
	PaneID string
	Status string
}

// watchRetry is how long a dropped subscription waits before dialing again. The panel works
// without events — it still ticks — so a reconnect loop must cost nothing while the server is down.
const watchRetry = 5 * time.Second

// Watch delivers Herdr's events until the context ends, reconnecting on its own when the
// connection drops. It is a one-way feed: every event means "look again", and nothing the panel
// does depends on receiving one, so a socket that never answers degrades to the panel's own tick.
//
// The socket is the session's, exported to every managed pane. A plugin pane that was started
// outside Herdr has none, and gets a channel that stays empty.
func Watch(ctx context.Context, socket string, subscriptions []Subscription) <-chan Notice {
	notices := make(chan Notice, 8)
	if socket == "" || len(subscriptions) == 0 {
		close(notices)
		return notices
	}
	go func() {
		defer close(notices)
		for ctx.Err() == nil {
			if err := stream(ctx, socket, subscriptions, notices); err != nil && ctx.Err() == nil {
				timer := time.NewTimer(watchRetry)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
		}
	}()
	return notices
}

// stream holds one connection for as long as it lasts. Closing the socket is what unblocks the
// read when the context ends: the protocol has no way to cancel a subscription in flight.
func stream(ctx context.Context, socket string, subscriptions []Subscription, notices chan<- Notice) error {
	connection, err := net.Dial("unix", socket)
	if err != nil {
		return fmt.Errorf("failed to reach the Herdr socket: %w", err)
	}
	defer func() { _ = connection.Close() }()
	closed := make(chan struct{})
	defer close(closed)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-closed:
		}
	}()
	request, err := json.Marshal(map[string]any{
		"id":     "herdr-review-loop",
		"method": "events.subscribe",
		"params": map[string]any{"subscriptions": subscriptions},
	})
	if err != nil {
		return fmt.Errorf("failed to encode the subscription: %w", err)
	}
	if _, err := connection.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("the Herdr event stream ended: %w", err)
		}
		var message struct {
			Event string `json:"event"`
			Data  struct {
				PaneID string `json:"pane_id"`
				Status string `json:"agent_status"`
			} `json:"data"`
		}
		if json.Unmarshal(line, &message) != nil || message.Event == "" {
			continue
		}
		notice := Notice{Kind: message.Event, PaneID: message.Data.PaneID, Status: message.Data.Status}
		select {
		case notices <- notice:
		case <-ctx.Done():
			return fmt.Errorf("the event watch ended: %w", ctx.Err())
		default:
			// a panel that has not caught up does not need the backlog: one wake-up is enough
		}
	}
}
