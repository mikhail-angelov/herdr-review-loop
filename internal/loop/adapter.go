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
	// Command is the in-pane command, typed into the agent's composer as one line. Its argument is
	// free text, so the round's instructions ride along in it; a command that wants its argument
	// for something else is not one this loop can delegate to.
	Command string
}

// adapters is the whole table. It is small on purpose, and review_command overrides any entry for
// any kind, known or not: a known kind whose CLI has moved is the common case, and an entry for an
// unknown kind is what makes that kind supported without a release.
//
// Verified against codex-cli 0.147.0: codex's in-pane `/review` reviews "the current code changes
// (staged, unstaged, and untracked files)", which is this loop's worktree scope exactly. It takes
// free-text instructions and has no level argument, so the round's level is expressed in that text.
// It renders its findings into the host UI rather than to a file, which is what the capture step is
// for.
//
// claude is deliberately not in this table, checked against Claude Code 2.1.236. Its `/code-review`
// starts a review of its own that fans out to subagents, which §2.2 puts out of scope, and it costs
// what fan-out costs. Worse for the contract: the findings then live in those subagents and in the
// host UI rather than in the pane session's own context, so the capture step has nothing dependable
// to serialize, and the command resolves its own scope, which silently drops the round's
// instructions. The built-in prompt asks one agent for the contract directly, so a claude reviewer
// takes that path. Put `review_command.claude` back in configuration if a build of that command
// reviews in-pane and reports into the session that ran it.
//
// One detail is taken from the documented CLI rather than from a live pane: `codex review [PROMPT]`
// accepts custom instructions as its argument, and the in-pane `/review` is assumed to do the same.
// If it does not, that kind's instructions are ignored rather than lost — the review still runs at
// codex's own default scope, which is this loop's, and the capture step still records it.
var adapters = map[string]Adapter{
	"codex": {Command: "/review"},
}

// NativeReview is one round's request to a reviewer that has its own review command, as the turns
// it has to be sent in.
type NativeReview struct {
	// Command is the line typed into the composer, the round's instructions in its argument.
	Command string
	// Capture is sent once the command's turn has settled. The findings are already in the agent's
	// context by then, so serializing them is cheap and lossless.
	Capture string
}

// Adapt resolves the native review request for one round, and reports false when this kind has no
// review command of its own — those fall back to the built-in prompt, which is the only place a
// review taxonomy of ours remains.
func Adapt(kind string, overrides map[string]string, policy RoundPolicy, scope Scope, reviewPath string, settled []Settled) (NativeReview, bool) {
	command, delegated := resolveCommand(kind, overrides, policy)
	if !delegated {
		return NativeReview{}, false
	}
	instructions := oneLine(nativeInstructions(policy, scope, settled))
	return NativeReview{Command: strings.TrimSpace(command + " " + instructions), Capture: CaptureReviewPrompt(reviewPath)}, true
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
		return Adapter{Command: slashed(command)}, true
	}
	adapter, known := adapters[kind]
	return adapter, known
}

// resolveCommand gives the round's command precedence over the kind's default or override. Naming
// a command for a round is enough to delegate it, even when the kind has no table entry.
func resolveCommand(kind string, overrides map[string]string, policy RoundPolicy) (string, bool) {
	if policy.Command != "" {
		return slashed(policy.Command), true
	}
	adapter, known := adapterFor(kind, overrides)
	if !known || adapter.Command == "" {
		return "", false
	}
	return adapter.Command, true
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
	command, delegated := resolveCommand(kind, overrides, policy)
	if !delegated {
		return "built-in review prompt"
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
