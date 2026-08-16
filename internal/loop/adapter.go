package loop

import (
	"fmt"
	"slices"
	"strings"
)

// Adapter maps one agent kind to the review command it already ships. The loop stops carrying a
// review taxonomy of its own: a hand-written prompt competes with, and loses to, the instruction
// set the reviewer already applies, and it competes for the same context with the project's own
// CLAUDE.md or AGENTS.md, which is where project-specific review standards belong.
//
// An adapter is expected to break when an agent CLI changes. That is why the degradation ladder
// keeps a markdown fallback and a reformat turn, and why parse_fallback is a recorded event: a
// kind whose adapter has rotted leaves a trail of them in events.jsonl.
type Adapter struct {
	// Command is the in-pane command, typed into the agent's composer as one line.
	Command string
	// LevelArgument means the command's own argument is the effort level, which leaves nowhere on
	// that line for the round's instructions. Where it is false the argument is free text and the
	// instructions ride along in it.
	LevelArgument bool
	// Levels are the values the command accepts. A round asking for something outside this set is
	// mapped to the middle of it rather than refused: the scale belongs to the agent, and a level
	// this build has not heard of is likelier new than wrong.
	Levels []string
}

// adapters is the whole table. It is small on purpose, and review_command overrides any entry for
// any kind, known or not: a known kind whose CLI has moved is the common case, and an entry for an
// unknown kind is what makes that kind supported without a release.
//
// Verified against codex-cli 0.147.0 and Claude Code 2.1.224:
//   - codex's in-pane `/review` reviews "the current code changes (staged, unstaged, and untracked
//     files)", which is this loop's worktree scope exactly. It takes free-text instructions and has
//     no level argument, so the round's level is expressed in that text.
//   - claude's `/code-review` takes its effort level as its own argument — low and medium narrow to
//     high-confidence findings, high and max broaden — which leaves the round's instructions to a
//     preceding turn. `ultra` is deliberately absent: it is multi-agent, billed, and user-triggered,
//     and fan-out is out of scope for this loop.
//
// Both render their findings into the host UI rather than to a file, which is what the capture step
// is for.
//
// One detail is taken from the documented CLI rather than from a live pane: `codex review [PROMPT]`
// accepts custom instructions as its argument, and the in-pane `/review` is assumed to do the same.
// If it does not, that kind's instructions are ignored rather than lost — the review still runs at
// codex's own default scope, which is this loop's, and the capture step still records it.
var adapters = map[string]Adapter{
	"codex":  {Command: "/review"},
	"claude": {Command: "/code-review", LevelArgument: true, Levels: []string{levelLow, levelMedium, levelHigh, levelMax}},
}

// NativeReview is one round's request to a reviewer that has its own review command, as the turns
// it has to be sent in. A slash command occupies its whole line, so a kind whose argument slot is
// already its level has nowhere to put the round's instructions; the loop sends them first, in the
// same session, rather than pretending every kind has one transport.
type NativeReview struct {
	// Preamble is sent before the command when it is non-empty.
	Preamble string
	// Command is the line typed into the composer.
	Command string
	// Capture is sent once the command's turn has settled. The findings are already in the agent's
	// context by then, so serializing them is cheap and lossless.
	Capture string
}

// Adapt resolves the native review request for one round, and reports false when this kind has no
// review command of its own — those fall back to the built-in prompt, which is the only place a
// review taxonomy of ours remains.
func Adapt(kind string, overrides map[string]string, policy RoundPolicy, scope Scope, reviewPath string, settled []Settled) (NativeReview, bool) {
	adapter, known := adapterFor(kind, overrides)
	if !known || adapter.Command == "" {
		return NativeReview{}, false
	}
	command := adapter.Command
	if policy.Command != "" {
		command = slashed(policy.Command)
	}
	instructions := nativeInstructions(policy, scope, settled)
	if adapter.LevelArgument {
		return NativeReview{
			Preamble: instructions,
			Command:  strings.TrimSpace(command + " " + adapter.level(policy.Level)),
			Capture:  CaptureReviewPrompt(reviewPath),
		}, true
	}
	return NativeReview{Command: strings.TrimSpace(command + " " + oneLine(instructions)), Capture: CaptureReviewPrompt(reviewPath)}, true
}

// adapterFor applies the per-kind override before the table, so a CLI that has moved is one
// setting away from working again. An override naming an empty command turns the native path off
// for that kind and returns it to the built-in prompt.
func adapterFor(kind string, overrides map[string]string) (Adapter, bool) {
	if command, overridden := overrides[kind]; overridden {
		command = strings.TrimSpace(command)
		if command == "" {
			return Adapter{}, false
		}
		// an override is free text, so its level cannot be assumed to be a separate argument
		return Adapter{Command: slashed(command)}, true
	}
	adapter, known := adapters[kind]
	return adapter, known
}

// level maps a round's level onto the ones this command accepts. An unknown level lands in the
// middle of the command's own scale rather than being refused.
func (a Adapter) level(level string) string {
	if len(a.Levels) == 0 {
		return ""
	}
	if slices.Contains(a.Levels, level) {
		return level
	}
	return a.Levels[len(a.Levels)/2]
}

// slashed keeps a configured command usable whether or not the user typed the leading slash. A
// command that is plainly not one — anything with a space — is left exactly as written.
func slashed(command string) string {
	if strings.HasPrefix(command, "/") || strings.ContainsAny(command, " \t") {
		return command
	}
	return "/" + command
}

// nativeInstructions is everything the loop still has to say to a reviewer that knows how to
// review: what is in scope, how hard to look this round, and what has already been settled.
func nativeInstructions(policy RoundPolicy, scope Scope, settled []Settled) string {
	blocks := []string{scope.Review(), policy.Review()}
	if block := settledBlock(settled); block != "" {
		blocks = append(blocks, block)
	}
	return strings.Join(blocks, "\n\n")
}

// oneLine folds instructions into the single line a slash command's argument occupies.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// DescribeCommand names the command a kind would run for a round, for the manifest, the dry run
// and `config`. It is the same resolution the run uses, so what those three print is what happens.
func DescribeCommand(kind string, overrides map[string]string, policy RoundPolicy) string {
	adapter, known := adapterFor(kind, overrides)
	if !known || adapter.Command == "" {
		return "built-in review prompt"
	}
	command := adapter.Command
	if policy.Command != "" {
		command = slashed(policy.Command)
	}
	if adapter.LevelArgument {
		return strings.TrimSpace(command + " " + adapter.level(policy.Level))
	}
	return command
}

// Kinds lists the agent kinds the table knows, plus any the configuration adds, in a stable order.
func Kinds(overrides map[string]string) []string {
	seen := map[string]bool{}
	kinds := make([]string, 0, len(adapters)+len(overrides))
	for kind := range adapters {
		seen[kind] = true
		kinds = append(kinds, kind)
	}
	for kind := range overrides {
		if !seen[kind] {
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

// CaptureReviewPrompt is the step that turns a native command's findings into this loop's data.
// The native commands render into the host UI rather than to a file, so without it the loop has
// nothing to give an author ids to decide on.
func CaptureReviewPrompt(reviewPath string) string {
	return fmt.Sprintf("Now record the review you just produced. Write it to %s as a single JSON object of exactly this shape, and write nothing else to that file — no prose, no code fences, no example:\n\n%s\n\n%s\n\nUse every finding you just reported, and add none. Do not review anything again and do not edit any code; only write that one file. Reply with just the path when you are done.", reviewPath, reviewSchema, reviewRules)
}
