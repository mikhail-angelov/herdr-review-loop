package loop

import (
	"fmt"
	"strings"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// RoundPolicy is one round's entry of the policy, resolved for the round that is about to run. It
// is the whole of what the loop still owns about how to review: no one-shot review command has a
// notion of "this is round three and we are only chasing regressions now".
type RoundPolicy struct {
	Number          int
	Level           string
	Command         string
	Instructions    string
	RegressionsOnly bool
	// Baseline is the absolute path to the concatenated diff of every prior author phase, named
	// for the reviewer to read. Empty when there is no baseline to judge regressions against,
	// which is what makes regressions_only a warning rather than an instruction on round 1.
	Baseline string
}

// Policy picks a round's entry out of the round policy. The last entry repeats for every round
// beyond the list, which is what lets a three-entry policy cover a ten-round budget.
func Policy(rounds []config.Round, number int) RoundPolicy {
	entry := config.Round{Level: levelHigh}
	if len(rounds) != 0 {
		index := min(max(number, 1), len(rounds)) - 1
		entry = rounds[index]
	}
	return RoundPolicy{
		Number:          number,
		Level:           strings.TrimSpace(entry.Level),
		Command:         strings.TrimSpace(entry.Command),
		Instructions:    strings.TrimSpace(entry.Instructions),
		RegressionsOnly: entry.RegressionsOnly,
	}
}

// The effort levels a round may ask for. They are the reviewers' own scale rather than one of
// ours: where a CLI has a level flag the adapter passes it, and where it does not the level is
// carried in the prompt instead. Only the transport differs.
const (
	levelMax    = "max"
	levelHigh   = "high"
	levelMedium = "medium"
	levelLow    = "low"
)

// Review is the block the reviewer is sent: the round's narrowing, then whatever else the policy
// has to say. Nothing here says what to look for — that belongs to the reviewer and to the
// project's own CLAUDE.md or AGENTS.md, which the reviewer reads itself.
func (p RoundPolicy) Review() string {
	blocks := []string{levelReview(p.Level)}
	if p.RegressionsOnly && p.Baseline != "" {
		blocks = append(blocks, fmt.Sprintf("Restrict this pass to what the fixes in the earlier rounds broke or left broken; do not raise anything that predates them. Those fixes are in %s — read it, and judge this pass against it. Mark what you find that way with \"regression\": true.", p.Baseline))
	}
	if p.Instructions != "" {
		blocks = append(blocks, p.Instructions)
	}
	return strings.Join(blocks, "\n\n")
}

// Fix narrows the author the same way Review narrows the reviewer, so the author is not asked to
// make broad changes in a round the reviewer is only closing out.
func (p RoundPolicy) Fix() string {
	switch p.Level {
	case levelMax, levelHigh:
		return "Apply the findings you agree with. Do not make broad changes for a low-severity finding, or for a medium-severity one whose fix would substantially expand the change — reject or defer those instead."
	case levelLow:
		return "Apply only high-severity findings and regressions caused by the earlier fixes. Reject or defer everything else."
	default:
		return "Apply remaining high-severity findings you agree with, and medium-severity ones whose fix is local. Reject or defer the rest."
	}
}

// Describe is the one line the manifest and the dry run carry per round.
func (p RoundPolicy) Describe() string {
	parts := []string{"level " + p.displayLevel()}
	if p.RegressionsOnly {
		parts = append(parts, "regressions only")
	}
	if p.Instructions != "" {
		parts = append(parts, "with instructions")
	}
	return strings.Join(parts, ", ")
}

// ReviewCommand names the reviewer's own command for this round, or the built-in prompt when the
// policy asks for no particular one.
func (p RoundPolicy) ReviewCommand() string {
	if p.Command != "" {
		return p.Command
	}
	return "built-in review prompt"
}

func (p RoundPolicy) displayLevel() string {
	if p.Level == "" {
		return levelHigh
	}
	return p.Level
}

// levelReview is how a level reaches a reviewer that has no effort flag of its own. An unknown
// level is treated as the middle of the scale rather than rejected: the scale belongs to the
// agents, and a level this build has not heard of is likelier new than wrong.
func levelReview(level string) string {
	switch level {
	case levelMax, levelHigh:
		return "This is a broad pass: report anything worth a look."
	case levelLow:
		return "Closure pass: report only what you are confident about, and anything of high severity."
	default:
		return "Narrower pass: verify the previous round's fixes, then report what is left that you are confident about."
	}
}
