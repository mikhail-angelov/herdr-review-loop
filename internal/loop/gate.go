package loop

import (
	"context"
	"fmt"
	"strings"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

// gate stops the run at a round boundary and waits for a person. It is the one place a run waits
// while nothing is wrong, and the agents are idle for its whole duration: a boundary is between
// two turns, so the tree can be read, edited by hand, and handed on to the next phase.
//
// It stops for three reasons — the configured gate, a pause the panel asked for, and a single step
// granted at the previous boundary — and the caller cannot tell them apart, because what happens
// next depends only on the answer.
func (r Run) gate(ctx context.Context, state *session, author herdr.Agent, kind, phase string, round int, summary string) (Choice, error) {
	stepping := state.stepPause
	state.stepPause = false
	if !stepping && !r.gateAt(kind) && !Paused(r.Environment.StateDir, author.WorkspaceID, state.record.Repository) {
		return Choice{Action: ChoiceContinue}, nil
	}
	pending := Pending{
		ID: requestID(state.record.ID, round, kind), Workspace: author.WorkspaceID,
		Repository: state.record.Repository, Run: state.record.ID, Round: round,
		Kind: kind, Summary: summary,
	}
	if err := publishPending(r.Environment.StateDir, pending); err != nil {
		return Choice{}, Exit(ExitTool, err)
	}
	defer clearPending(r.Environment.StateDir)
	_ = state.archive.Event(round, phase, EventPaused, summary)
	_ = r.Log.Write(fmt.Sprintf("paused after the round %d %s: %s", round, phase, summary))
	r.reportProgress(ctx, author.WorkspaceID, PhasePaused, round)
	r.label(ctx, author, "author", "paused: waiting for you")
	r.notify(ctx, "Review loop paused", summary, soundRequest)
	choice, err := awaitChoice(ctx, r.Environment.StateDir, pending)
	if err != nil {
		return Choice{}, err
	}
	if choice.Action == ChoiceStep {
		state.stepPause = true
	}
	if choice.Action == ChoiceInstruct {
		state.instruction = choice.Text
	}
	_ = state.archive.Event(round, phase, EventResumed, choice.Action)
	_ = r.Log.Write("resumed: " + choice.Action)
	return choice, nil
}

// gateAt reports whether the configuration stops at this boundary on its own.
func (r Run) gateAt(kind string) bool {
	switch r.Config.Gate {
	case config.GateBoth:
		return true
	case config.GateReview:
		return kind == PendingGateReview
	case config.GateRound:
		return kind == PendingGateRound
	default:
		return false
	}
}

// takeInstruction spends what the user typed at a boundary. It is taken rather than read, because
// an instruction meant for one author turn that survived into the next would be a standing order
// nobody gave.
func (s *session) takeInstruction() string {
	instruction := s.instruction
	s.instruction = ""
	return instruction
}

// ReviewSummary is one round's findings in a line: what a person needs to decide whether to look
// closer before the author starts changing code.
func ReviewSummary(review Review) string {
	if len(review.Findings) == 0 {
		return "no findings"
	}
	counts := map[string]int{}
	for _, finding := range review.Findings {
		counts[finding.Severity]++
	}
	parts := make([]string, 0, 3)
	for _, severity := range []string{SeverityHigh, SeverityMedium, SeverityLow} {
		if counts[severity] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[severity], severity))
		}
	}
	return fmt.Sprintf("%d finding(s): %s", len(review.Findings), strings.Join(parts, " · "))
}
