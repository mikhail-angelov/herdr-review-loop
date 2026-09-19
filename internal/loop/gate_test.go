package loop

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// awaitPending blocks until the run publishes a boundary, failing rather than hanging when it
// never does. It watches the run's own error channel, so a run that ended is reported as that
// instead of as a timeout.
func awaitPending(t *testing.T, done <-chan error, state, repository string) Pending {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if pending, waiting := PendingRequest(state, "workspace", repository); waiting {
			return pending
		}
		select {
		case err := <-done:
			t.Fatalf("the run ended without waiting at a boundary: %v", err)
		case <-deadline.C:
			t.Fatal("the run never waited at a boundary")
		case <-tick.C:
		}
	}
}

func TestGateStopsBeforeTheAuthorTouchesTheCodeAndStopEndsTheRun(t *testing.T) {
	client := &runClient{
		agents:  pairedAgents(),
		reviews: []string{`{"status":"findings","findings":[{"file":"a.go","title":"fix me"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	values.Gate = config.GateReview
	run, repository, state := newRun(t, client, values)
	done := make(chan error, 1)
	go func() { done <- run.Execute(context.Background(), false) }()
	pending := awaitPending(t, done, state, repository)
	if pending.Kind != PendingGateReview || !strings.Contains(pending.Summary, "1 finding(s)") {
		t.Fatalf("pending %#v", pending)
	}
	if err := Answer(state, pending, ChoiceStop, ""); err != nil {
		t.Fatal(err)
	}
	if err := <-done; ExitCode(err) != ExitCanceled {
		t.Fatalf("got %v (code %d), want a canceled run", err, ExitCode(err))
	}
	if got := client.prompted; !slices.Equal(got, []string{"reviewer"}) {
		t.Fatalf("prompted %#v, want the author never to have been asked", got)
	}
	report := latestReport(t, state)
	if len(report.Findings) != 1 || report.Findings[0].Action != ActionUnreviewed {
		t.Fatalf("report %#v", report.Findings)
	}
}

func TestAnInstructionGivenAtAGateReachesTheAuthorOnce(t *testing.T) {
	client := &runClient{
		agents:    pairedAgents(),
		reviews:   []string{`{"status":"findings","findings":[{"file":"a.go","title":"fix me"}]}`, `{"status":"clean","findings":[]}`},
		decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"done"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	values.Gate = config.GateReview
	run, repository, state := newRun(t, client, values)
	done := make(chan error, 1)
	go func() { done <- run.Execute(context.Background(), false) }()
	pending := awaitPending(t, done, state, repository)
	if err := Answer(state, pending, ChoiceInstruct, "leave the logging alone"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("got %v, want a clean second round", err)
	}
	instructed := 0
	for _, prompt := range client.prompts {
		if strings.Contains(prompt, "leave the logging alone") {
			instructed++
		}
	}
	if instructed != 1 {
		t.Fatalf("the instruction reached %d prompts, want exactly the one author turn", instructed)
	}
}

func TestAPauseAskedForFromThePanelIsHonoredAtTheNextBoundary(t *testing.T) {
	client := &runClient{
		agents:    pairedAgents(),
		reviews:   []string{`{"status":"findings","findings":[{"file":"a.go","title":"fix me"}]}`, `{"status":"clean","findings":[]}`},
		decisions: []string{`{"decisions":[{"id":"r01-1","action":"applied","note":"done"}]}`},
	}
	values := quickValues()
	values.MaxIterations = 2
	run, repository, state := newRun(t, client, values)
	if err := SetPause(state, "workspace", repository, true); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- run.Execute(context.Background(), false) }()
	pending := awaitPending(t, done, state, repository)
	if pending.Kind != PendingGateReview {
		t.Fatalf("pending %#v, want the first boundary after the review", pending)
	}
	if err := Answer(state, pending, ChoiceContinue, ""); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("got %v, want the run to continue to a clean round", err)
	}
	if Paused(state, "workspace", repository) {
		t.Fatal("the honored pause request outlived the boundary")
	}
}
