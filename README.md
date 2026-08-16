# herdr-review-loop

[![CI](https://github.com/mikhail-angelov/herdr-review-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/mikhail-angelov/herdr-review-loop/actions/workflows/ci.yml)

**Have a second AI agent review the first one's code — automatically, round after
round, until the review comes back clean.**

You are pairing with an agent in a Herdr pane. It just wrote a few hundred lines.
Reviewing them yourself is slow, and asking the *same* agent to review its own work
mostly gets you agreement. `herdr-review-loop` hands the diff to a **different agent
kind** in the same workspace — Claude reviews Codex, Codex reviews Claude — and then
drives the two of them back and forth for you:

```
reviewer  →  writes findings as JSON, each one identified by the loop
   author →  applies what it agrees with, decides every finding it was given
reviewer  →  re-reviews, narrower this time, told what is already settled
   …        until the review is clean, the round budget runs out, or an agent gets stuck
```

One keypress starts it. A side panel shows the round and a live event stream. You go do
something else.

**Nothing the loop writes lands in your working tree.** The exchange happens under
`.review-loop/run/`, which the loop hides from git for this clone alone, so the reviewer
never sees the loop's own files and every finding is archived where a finished run can
still be explained a week later.

<img width="2620" height="1446" alt="image" src="https://github.com/user-attachments/assets/86c3cf1d-c838-4c70-a52d-c9b89aeb3189" />

## Why it works better than "review your own diff"

- **A different model, with different blind spots.** The reviewer is picked from another
  agent kind by default, so you get an actual second opinion instead of an echo.
- **Both sessions are wiped before every turn.** No context rot, no agent talking itself
  into approving its earlier fix. Each round starts cold on the current diff.
- **Findings are data, not prose.** The reviewer writes JSON; the loop assigns each finding
  an id the author must decide on and a fingerprint that follows it across rounds. A
  garbled turn costs one reformat, not a whole round.
- **Decisions are durable, arguments are not.** The author decides every id — applied,
  rejected or deferred, with a reason. The next reviewer is handed the refusals and told
  not to re-litigate them.
- **It converges instead of wandering.** Round 1 reviews broadly; round 2 only chases
  remaining high/medium issues; round 3+ reports only regressions and high-severity
  findings. Reviews get narrower, not longer.
- **It is honest about failure.** Exit 0 only on a clean review, read from output the loop
  could parse, that agrees with itself, and that asks nothing. A spent budget, a broken
  setup, a question for a human, a cancellation and an agent that gave up are five
  different exit codes.

Scope is always the **uncommitted working tree** — so it fits right before you commit.

## Requirements

Herdr 0.7.5 or newer, on macOS or Linux, with **two agents of different kinds in the same
workspace** (for example a `claude` pane and a `codex` pane). No Node. No Go — releases
ship a prebuilt binary; a Go 1.26 toolchain is needed only to build an unreleased
checkout.

## Quick start

```sh
herdr plugin install mikhail-angelov/herdr-review-loop
herdr server reload-config
```

Add a keybinding (see [Keybindings](#keybindings-and-panes) for the full set):

```toml
[[keys.command]]
key = "prefix+alt+r"
type = "plugin_action"
command = "herdr-review-loop.review"
description = "review loop"
```

Then, from the pane of the agent that wrote the code:

1. Press `prefix+alt+r`. That agent becomes the **author**; a different-kind agent in the
   workspace becomes the **reviewer**.
2. Watch the panel that opens beside you, or keep working elsewhere.
3. When it finishes, your working tree holds the fixes and nothing else. Run
   `herdr-review-loop show` — or press `enter` in the history pane — to read what each
   round found and what the author did about it.
4. Press `f` in the panel to **finish**: the run directory is removed and the panel closes.
   The archive under the plugin state directory stays.

Not sure who would be paired? `review --dry-run` prints the pair, the scope, the profile
and the per-round policy, and touches nothing.

For development instead of install:

```sh
make install-plugin   # build, then herdr plugin link .
make prep             # fmt + vet + lint + test — run before every commit
```

`make prep` needs [golangci-lint](https://golangci-lint.run/welcome/install/) v2.

## Commands

| Command | What it does |
| --- | --- |
| `review` | Runs the reviewer → author → reviewer loop. Exits 0 only when the review is clean. |
| `review --dry-run` | Prints the pair, scope, profile and round policy without taking a lock or writing files. |
| `show [--run ID] [--round N] [--format md\|json]` | Renders an archived round — by default the last round of this repository's most recent run. |
| `stop` | Cancels the active loop from any pane. |
| `finish` | Closes out a review: removes the run directory and closes the panel. |
| `open-panel` / `open-settings` / `open-history` | Opens the corresponding pane. |

### Exit codes

`review` is the only command with a code worth branching on:

| Code | Meaning |
| --- | --- |
| 0 | Clean review. |
| 1 | Findings remain and the round budget is spent. A normal outcome, not a failure. |
| 2 | Tool error: configuration, no second agent, an archive that cannot be written. |
| 3 | A human is needed: an agent is blocked, or the reviewer returned open questions. |
| 4 | Canceled through `stop`. |
| 5 | Terminal agent failure: a stall, a crash, or output that never parsed. Usually fixed by rerunning. |

## Where a run puts things

```
<repo>/.review-loop/              committed by the project, if it wants to
└── run/                          the active run — created 0700, hidden from git
    └── round-01/
        ├── review.json           the reviewer's findings, then the same with ids added
        └── decisions.json        what the author decided about each id

<state>/history/<run-id>/         the archive, rotated after archive.keep runs
├── manifest.json                 the resolved configuration, with provenance
├── events.jsonl                  phase starts, retries, degradations, blocks
├── round-01/
│   ├── prompt-review.md          what the reviewer was sent, verbatim
│   ├── review.raw.txt            what it answered, verbatim
│   ├── review.json               parsed, with ids and fingerprints
│   ├── prompt-fix.md / fix.raw.txt / decisions.json
│   └── changes.patch             everything the author phase changed, new files included
└── report.json                   every finding the run raised and what became of it
```

The run directory is kept out of git by one line appended to `.git/info/exclude`, which is
per-clone and untracked — no file you own or commit is modified, and the project is free to
commit `.review-loop/` itself. This is what makes the loop's own artifacts invisible to the
reviewer and absent from the round checkpoints.

If a repository has somehow **committed** something under `.review-loop/run/`, `review`
refuses to start (exit 2) and names the `git rm -r --cached` that fixes it: `info/exclude`
cannot ignore what git already tracks, and pretending otherwise would put the loop's files
back in the reviewed diff.

`changes.patch` answers the one question nothing else answers: is the loop converging, or
moving the same lines back and forth.

## Finishing a review

`finish` removes the run directory and closes the panel. The run archived itself as it
went, so nothing is at risk in the removal — and your code changes are never touched:

```
run 2026-08-09 19:12 · claude @ w5:p1 ← codex @ w5:p2 · 3 round(s) · clean
findings: 4 applied · 2 rejected · 1 deferred · 0 missing · 0 unreviewed
archived to history/2026-08-09T19-12-04-000000000Z
removed .review-loop/run/
```

The counts matter more than they look. A clean verdict does not by itself mean anything
was fixed — the author may have rejected every finding, and later rounds treat rejected
decisions as closed. `4 applied · 2 rejected` is the difference between a review that
converged and one that was talked out of. `missing` and `unreviewed` are findings nobody
answered, which is exactly the case a report that only listed decided findings would hide.

`finish` never commits anything and refuses while a loop is running (`stop` it first).

## History and checkpoints

Every run is recorded under the plugin's state directory, and — in a git repository —
each round's working tree is snapshotted so you can see what that round actually changed.
`open-history` (or `h` in the panel) opens a browser over both:

```
 herdr-review-loop · history
 > 2026-08-09 19:12 · claude @ w5:p1 ← codex @ w5:p2 · 3 round(s) · clean
     round 1   6 finding(s) · 4 applied    diff
   > round 2   2 finding(s) · 1 applied    diff
     round 3   clean                       —
   2026-08-09 14:40 · claude @ w5:p1 ← codex @ w5:p2 · 1 round(s) · canceled

   enter findings · d round diff · a run diff · c restore · esc back · q close
```

`enter` shows that round's findings, `d` the diff that round produced, `a` the whole run's
diff. Findings are rendered from the archive by the same function `show` uses, so the pane
and the command can never disagree. Both open in your own pager — `$PAGER` for findings,
git's for diffs — so scrolling,
search and highlighting are whatever you already have configured. `c` prints a `git
restore` command rather than running it: rolling back is the one destructive thing here,
it cannot be made exact without also deleting files created after the checkpoint, and that
call is yours.

Checkpoints are written with a temporary index, so they never touch your worktree, your
staged changes, the stash stack, or `HEAD`, and they take no `index.lock` — an agent
running git at the same moment cannot collide with one. They live under
`refs/herdr-review-loop/`, invisible to `git branch` and `git log`. The last **5** runs per
repository keep theirs; older refs are dropped at the start of a run so git can collect
the objects. Runs in a directory that is not a git work tree simply have no diffs, and the
findings remain readable.

## The review contract

The reviewer writes one JSON object to `round-NN/review.json`:

```json
{
  "status": "findings",
  "findings": [
    {
      "file": "internal/loop/run.go", "line": 88, "end_line": 0,
      "category": "correctness", "severity": "high", "verdict": "confirmed",
      "title": "context is not canceled on the timeout path",
      "body": "one to three sentences on why this is a problem",
      "fix": "what to do about it",
      "regression": false
    }
  ],
  "open_questions": [{"question": "is the 5m budget meant to cover a reformat turn too?"}],
  "pre_existing": []
}
```

`severity` and `verdict` are optional and default to `medium` and `confirmed`. `category`
is the reviewer's own slug — the loop never branches on it, it only carries it through to
the report. `pre_existing` findings are recorded and never sent to the author.

**Ids and fingerprints belong to the loop.** Models do not hold stable identifiers across
turns, so they are not asked to. Before the author sees the file, the loop rewrites it with
`id` (`r01-3`, unique in a run) and `fingerprint` (a hash of file, category and normalized
title) on every finding. The id is what the author decides on; the fingerprint is how the
same finding is recognized in a later round of the same run.

The author answers with `round-NN/decisions.json`:

```json
{
  "tests": {"ran": true, "outcome": "go test ./... passed"},
  "decisions": [
    {"id": "r01-1", "action": "applied",  "note": "fixed, test added"},
    {"id": "r01-2", "action": "rejected", "note": "false positive: ctx is canceled above"}
  ]
}
```

Every id must be decided. One that is not gets `{"action": "missing"}` written by the loop
and shows up in the report; an author that decides nothing at all stops the run, because a
round that changed nothing and recorded nothing would repeat forever. Rejected and deferred
findings are carried into the next round's request with their reasons attached.

**The loop resolves contradictions, not the model.** `clean` with a non-empty findings
array is dirty — the array wins. `findings` with an empty array is a truncated turn, not
clean code, and never produces exit 0. A non-empty `open_questions` array ends the run with
exit 3 before any author phase, whatever the status says: a reviewer that has to ask cannot
also be read as clean, and an author cannot answer a question addressed to a human.

**Unreadable output costs a turn, not a round.** The loop parses the last top-level JSON
object in the file (last, because models like to show an example and then give the real
answer), falls back to the v1 markdown form, and failing that sends one reformat turn to
the same agent — whose context still holds the review it just wrote. Only if that fails too
does the phase reset the session and spend a retry, and an exhausted budget exits 5. Every
step is recorded in `events.jsonl`, so a reviewer whose output has drifted leaves a trail
rather than a mystery.

Both sessions are reset before their turns. The reviewer reads the current uncommitted diff
and the decision journal of prior rounds; the author reads the findings and the diff. This
keeps contexts small and prevents re-litigating recorded decisions.

The reviewer is **never told what to look for** beyond how narrow this round is. A
hand-written taxonomy competes with the instruction set the reviewer already applies, and
with your project's own `CLAUDE.md` or `AGENTS.md` — which is where project-specific review
standards belong, because every review benefits from them there, not only this one.

## Keybindings and panes

Bind the actions in your Herdr keybindings, for example:

```toml
# Replace the key chords with your preferred bindings.
[[keys.command]]
key = "prefix+alt+r"
type = "plugin_action"
command = "herdr-review-loop.review"
description = "review loop"

[[keys.command]]
key = "prefix+alt+x"
type = "plugin_action"
command = "herdr-review-loop.stop"
description = "stop review loop"

[[keys.command]]
key = "prefix+alt+p"
type = "plugin_action"
command = "herdr-review-loop.panel"
description = "review loop panel"

[[keys.command]]
key = "prefix+alt+comma"
type = "plugin_action"
command = "herdr-review-loop.settings"
description = "review loop settings"

[[keys.command]]
key = "prefix+alt+f"
type = "plugin_action"
command = "herdr-review-loop.finish"
description = "finish review"

[[keys.command]]
key = "prefix+alt+h"
type = "plugin_action"
command = "herdr-review-loop.history"
description = "review history"
```

The panel uses `r` to start a review, `x` to cancel it, `f` to finish one, `h` for
history, `s` for settings, and `q` to close. Settings use `j`/`k` or arrows to select,
Enter to edit, `d` to restore a default, `s` to save, `x` to cancel the loop, and `q` to
close. History uses `j`/`k` to move, Enter to open a run and then its findings, `d` and
`a` for diffs, `c` for a restore command, `esc` to go back, and `q` to close. `stop` is
safe from any pane. The panel is a detached process, so closing it never cancels a review
in progress.

## Configuration

Configuration is stored in Herdr's plugin config directory as `config.json`, and is
editable from the settings pane. Durations use Go notation such as `30m` or `90s`.

```json
{
  "reviewer": { "kind": "", "name": "" },
  "max_iterations": 10,
  "timeouts": { "review": "30m", "fix": "30m" },
  "retries": 1,
  "archive": { "keep": 20, "raw_output": true },
  "reset_command": ""
}
```

| Key | Default | Meaning |
| --- | --- | --- |
| `reviewer.kind` | empty | Reviewer agent kind; empty chooses a different kind. |
| `reviewer.name` | empty | Reviewer name or pane id; overrides `reviewer.kind`. |
| `max_iterations` | `10` | Maximum review rounds. |
| `timeouts.review` | `30m` | Budget for one review phase. |
| `timeouts.fix` | `30m` | Budget for one author fix phase. |
| `retries` | `1` | Repeat attempts per phase *after* the first, 0 to 5. |
| `archive.keep` | `20` | How many finished runs keep their archive. Older ones are dropped whole at the start of a run. |
| `archive.raw_output` | `true` | Keep each agent's verbatim output in the archive. Set to `false` and no `*.raw.txt` is written — including for a round that failed to parse. |
| `reset_command` | empty | Reset command for agent kinds without a built-in command. |

Invalid values and unknown keys are ignored with a warning; the loop uses the default
instead. The settings pane validates values before saving them. `stop` has to work with a
broken `config.json`, so a malformed setting is never a failure.

Archive size is bounded by `archive.keep` and by nothing else, and raw model output is the
bulk of it. Those are the two dials.

Reset commands are built in for `claude` and `gemini` (`/clear`) and for `codex` and
`opencode` (`/new`). Set `reset_command` only for an agent kind that is not one of those.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `run this from your agent's pane` | The loop was started from a pane with no agent in it. |
| No reviewer found | The workspace has no second agent, or only agents of the author's own kind. Open one, or pin one with `reviewer_name`. |
| The loop stops saying an agent is blocked | That agent is waiting on a question. Answer it, then run the loop again. |
| A review never finishes | Raise `timeouts.review` / `timeouts.fix`, or cancel with `stop` from any pane. |
| `review` exits 2 naming tracked files | The repository has committed something under `.review-loop/run/`. Run the `git rm -r --cached` the message prints. |
| `review` exits 5 | An agent stalled, crashed, or never produced output the loop could read. `events.jsonl` in the run's archive says which. Usually fixed by rerunning. |
| `finish` refuses | A loop still holds the run lock. `stop` it, then finish. |
| History shows `—` for every round | The repository is not a git work tree, or the run's checkpoints have aged out of the retention window. |

Logs live in the plugin state dir and are visible with
`herdr plugin log list --plugin herdr-review-loop`. Everything about a run is under
`history/<run-id>/` — start with `report.json` for what the run did, and `events.jsonl`
for what happened to it.

## A note on the review policy

The round policy, and any `.review-loop/` a project commits, are read from the working
copy like every other file — including when the diff under review is the one editing them.
Freezing them would be theatre: the same branch can redirect the review just as effectively
through `CLAUDE.md` or `AGENTS.md`, which the reviewer reads itself and nothing here can
freeze. **The review policy is a convenience, not a boundary, and a diff that touches it
deserves a human's eye.**
