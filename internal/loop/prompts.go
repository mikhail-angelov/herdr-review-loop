package loop

import (
	"fmt"
	"strings"
)

// reviewSchema is the contract, written once and quoted by every prompt that has to state it. It
// is the schema of §4.1 with the two loop-owned keys left out, because the loop assigns those.
const reviewSchema = `{
  "status": "clean" | "findings",
  "findings": [
    {
      "file": "path/relative/to/the/repository.go",
      "line": 88,
      "end_line": 0,
      "category": "your own short slug",
      "severity": "high" | "medium" | "low",
      "verdict": "confirmed" | "plausible",
      "title": "one line naming the problem",
      "body": "one to three sentences on why this is a problem",
      "fix": "what to do about it",
      "regression": false
    }
  ],
  "open_questions": [{"question": "one sentence", "file": "optional/path.go", "line": 0}],
  "pre_existing": []
}`

// decisionsSchema is the author's half of the same contract.
const decisionsSchema = `{
  "tests": {"ran": true, "outcome": "go test ./... passed"},
  "decisions": [
    {"id": "r01-1", "action": "applied", "note": "fixed, test added"},
    {"id": "r01-2", "action": "rejected", "note": "false positive: ctx is canceled in the defer above"},
    {"id": "r01-3", "action": "deferred", "note": "needs an interface change; separate task"}
  ]
}`

// reviewRules explain the fields whose meaning a reader could not guess. Everything about *what to
// look for* is deliberately absent: the project's own review standards live in its agent
// configuration, where every review benefits from them and not only this loop.
const reviewRules = `- "file" is relative to the repository root. "line" 0 means the file as a whole; "end_line" 0 means the finding is one line.
- "category" is your own slug; nothing branches on it.
- "severity" and "verdict" may be omitted; they default to medium and confirmed. Order the array by how much each finding matters — that order is kept.
- "pre_existing" holds findings that were already true before these changes. They are recorded and not acted on.
- "open_questions" holds anything you could not settle from the diff and that needs a human. A non-empty array ends the run before any fix is attempted, so use it only when you genuinely cannot proceed.
- Use "clean" only when nothing is left that is worth changing, and then leave "findings" empty. "findings" with an empty array is read as a truncated turn, never as clean.`

// ReviewPrompt asks the reviewer for this round's findings. The loop does not describe what to look
// for beyond the round's own narrowing: that taxonomy belongs to the reviewer and to the project's
// CLAUDE.md or AGENTS.md, which the reviewer reads itself.
func ReviewPrompt(scope Scope, policy RoundPolicy, maximum int, reviewPath string, settled []Settled) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "You are the reviewer in an automated review loop. This is round %d of %d, and your session was cleared beforehand.\n\n", policy.Number, maximum)
	prompt.WriteString(scope.Review() + "\n\n")
	prompt.WriteString(policy.Review() + "\n\n")
	if block := settledBlock(settled); block != "" {
		prompt.WriteString(block + "\n\n")
	}
	fmt.Fprintf(&prompt, "When the review is finished, write your findings to %s as a single JSON object of exactly this shape:\n\n%s\n\n%s\n\n", reviewPath, reviewSchema, reviewRules)
	prompt.WriteString("Write nothing but that object to that file, and change no code — the file is your only output. Reply with just the path when you are done.")
	return prompt.String()
}

// settledBlock carries the decision journal forward. Rejected and deferred points arrive with the
// reason they were refused, and one instruction, so a round is not spent relitigating them.
func settledBlock(settled []Settled) string {
	if len(settled) == 0 {
		return ""
	}
	var block strings.Builder
	block.WriteString("These points are already settled — do not raise them again. If one of these reasons is wrong, say so explicitly and once, in that finding's body:\n")
	for _, entry := range settled {
		fmt.Fprintf(&block, "- %s in round %d: %s — %s\n", entry.Action, entry.Round, entry.Title, entry.Note)
	}
	return strings.TrimRight(block.String(), "\n")
}

// ReviewReformatPrompt is step 3 of the degradation ladder: one turn, to the same agent, whose
// context still holds the review it just produced. It is what stops a garbled turn from costing a
// whole round.
func ReviewReformatPrompt(reviewPath, reason string) string {
	return fmt.Sprintf("%s could not be read as this loop's review format: %s.\n\nWrite your review again to that same path as a single JSON object of exactly this shape, and add nothing else — no prose, no code fences, no example:\n\n%s\n\n%s\n\nDo not review anything again and do not edit any code; only rewrite that one file.", reviewPath, reason, reviewSchema, reviewRules)
}

// FixPrompt asks the author to act on the findings and decide every one of them. The decision
// record is the proof of work, and it is what the next round's reviewer is shown.
func FixPrompt(scope Scope, policy RoundPolicy, reviewPath, decisionsPath string, ids []string) string {
	return FixPromptWithInstruction(scope, policy, reviewPath, decisionsPath, ids, "")
}

// FixPromptWithInstruction adds the user's explicit one-shot choice to the normal author task.
func FixPromptWithInstruction(scope Scope, policy RoundPolicy, reviewPath, decisionsPath string, ids []string, instruction string) string {
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "You are the author in round %d of an automated review loop and have a fresh session.\n\n", policy.Number)
	fmt.Fprintf(&prompt, "Read %s. It holds this round's findings, each with an \"id\".\n\n", reviewPath)
	if instruction != "" {
		prompt.WriteString("User instruction for this one-shot review:\n" + instruction + "\n\n")
	}
	prompt.WriteString(scope.Author() + "\n\n")
	prompt.WriteString(policy.Fix() + "\n\n")
	fmt.Fprintf(&prompt, "Then write %s as a single JSON object of exactly this shape:\n\n%s\n\n", decisionsPath, decisionsSchema)
	fmt.Fprintf(&prompt, "\"action\" is one of applied, rejected or deferred, each with a short note saying why. Decide every id exactly once: %s\n\n", strings.Join(ids, ", "))
	fmt.Fprintf(&prompt, "Record in \"tests\" whether you ran the build and the test suite and what came of it. Do not edit %s.", reviewPath)
	return prompt.String()
}

// OneShotBriefPrompt has the author present the review to its user without starting fixes.
func OneShotBriefPrompt(reviewPath string) string {
	return fmt.Sprintf("A reviewer has completed one review pass. Read %s, then summarize its findings to the user in your reply. Do not edit code and do not start fixing anything yet. Tell the user that they can choose Apply, Cancel, or a custom instruction in the review-loop panel.", reviewPath)
}

// OneShotActionPrompt closes a clean one-shot review after the user has made a panel choice.
func OneShotActionPrompt(choice OneShotChoice) string {
	return fmt.Sprintf("The user selected %q for the completed one-shot review. %s Do not edit code unless the custom instruction explicitly asks you to. Acknowledge the selection, then stop; this one-shot review ends after this turn.", choice.Action, choice.Text)
}

// FixReformatPrompt is the author's half of ladder step 3.
func FixReformatPrompt(decisionsPath, reason string, ids []string) string {
	return fmt.Sprintf("%s could not be read as this loop's decision format: %s.\n\nWrite it again as a single JSON object of exactly this shape, and add nothing else — no prose, no code fences, no example:\n\n%s\n\nDecide every id exactly once: %s\n\nDo not change any code in this turn; only rewrite that one file.", decisionsPath, reason, decisionsSchema, strings.Join(ids, ", "))
}
