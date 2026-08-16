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
reviewer  →  writes review.md  ("STATUS: FINDINGS" + a list)
   author →  applies what it agrees with, records the rest in review-summary.md
reviewer  →  re-reviews, narrower this time
   …        until "STATUS: CLEAN", the round budget runs out, or an agent gets stuck
```

One keypress starts it. A side panel shows the round and a live log. You go do
something else.

<img width="2620" height="1446" alt="image" src="https://github.com/user-attachments/assets/86c3cf1d-c838-4c70-a52d-c9b89aeb3189" />

## Why it works better than "review your own diff"

- **A different model, with different blind spots.** The reviewer is picked from another
  agent kind by default, so you get an actual second opinion instead of an echo.
- **Both sessions are wiped before every turn.** No context rot, no agent talking itself
  into approving its earlier fix. Each round starts cold on the current diff.
- **Decisions are durable, arguments are not.** The author records applied, rejected, and
  deferred findings in `review-summary.md`. The next reviewer reads it and doesn't
  re-litigate settled points.
- **It converges instead of wandering.** Round 1 reviews broadly; round 2 only chases
  remaining high/medium issues; round 3+ reports only regressions and high-severity
  findings. Reviews get narrower, not longer.
- **It is honest about failure.** Exit 0 only on a clean review. Budget spent, agent
  blocked on a question, or an author that stopped recording decisions all stop the loop
  and tell you why.

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
3. When it finishes you have `review.md` (the last verdict) and `review-summary.md` (what
   was applied, rejected, or deferred, and why) — plus the actual fixes in your tree.
4. Read them, then press `f` in the panel to **finish**: the run's decisions are archived,
   both scaffolding files are deleted, and the panel closes. Your code changes stay.

Not sure who would be paired? `review --dry-run` prints the pair and touches nothing.

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
| `review --dry-run` | Prints the author and reviewer without taking a lock or writing files. |
| `stop` | Cancels the active loop from any pane. |
| `finish` | Closes out a review: archives the decisions, deletes the two loop files, closes the panel. |
| `open-panel` / `open-settings` / `open-history` | Opens the corresponding pane. |

## Finishing a review

A run leaves two files in your working tree, and they are scaffolding rather than results
— the result is the code. `finish` clears them:

```
run 2026-08-09 19:12 · claude @ w5:p1 ← codex @ w5:p2 · 3 round(s) · clean
decisions: 4 applied · 2 rejected · 1 deferred
tests: go test ./... passed
decisions archived to history/2026-08-09T19-12-04Z/summary.md
removed review.md, review-summary.md
```

The counts matter more than they look. A clean verdict does not by itself mean anything
was fixed — the author may have rejected every finding, and round 2 onward treats rejected
decisions as closed. `4 applied · 2 rejected` is the difference between a review that
converged and one that was talked out of.

`finish` never commits anything and never touches a file the plugin did not create. It
refuses while a loop is running (`stop` it first), archives the decision record before
deleting it, and closes the panel last.

## History and checkpoints

Every run is recorded under the plugin's state directory, and — in a git repository —
each round's working tree is snapshotted so you can see what that round actually changed.
`open-history` (or `h` in the panel) opens a browser over both:

```
 herdr-review-loop · history
 > 2026-08-09 19:12 · claude @ w5:p1 ← codex @ w5:p2 · 3 round(s) · clean
     round 1   6 finding(s)    diff
   > round 2   2 finding(s)    diff
     round 3   clean           —
   2026-08-09 14:40 · claude @ w5:p1 ← codex @ w5:p2 · 1 round(s) · canceled

   enter findings · d round diff · a run diff · c restore · esc back · q close
```

`enter` shows that round's findings, `d` the diff that round produced, `a` the whole run's
diff. Both open in your own pager — `$PAGER` for findings, git's for diffs — so scrolling,
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

The reviewer writes `review.md` by default. Its first non-empty line must be either
`STATUS: CLEAN` or `STATUS: FINDINGS`; any other result is treated as findings, so the
loop errs toward another review. Findings are one per bullet:

```
- [high|medium|low] path/to/file.ext:LINE — what is wrong — what to do about it
```

The author must then rewrite `review-summary.md` with the compact record of decisions, one
per bullet, each beginning with `applied:`, `rejected:`, or `deferred:`, and a final
`tests:` line recording whether the build and test suite were run. The loop stops if the
author does not update that file — a round that changes nothing and records nothing is a
round that would repeat forever.

Both sessions are reset before their turns. The reviewer reads the current uncommitted
diff and `review-summary.md`; the author reads the review, the summary, and the diff.
This keeps contexts small and prevents re-litigating recorded decisions.

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

| Key | Default | Meaning |
| --- | --- | --- |
| `reviewer_kind` | empty | Reviewer agent kind; empty chooses a different kind. |
| `reviewer_name` | empty | Reviewer name or pane id; overrides `reviewer_kind`. |
| `max_iterations` | `10` | Maximum review rounds. |
| `review_file` | `review.md` | Reviewer output, relative to the repository. |
| `review_timeout` | `30m` | Budget for one review phase. |
| `fix_timeout` | `30m` | Budget for one author fix phase. |
| `reset_command` | empty | Reset command for agent kinds without a built-in command. |

Invalid values and unknown keys are ignored with a warning; the loop uses the default
instead. The settings pane validates values before saving them.

Reset commands are built in for `claude` and `gemini` (`/clear`) and for `codex` and
`opencode` (`/new`). Set `reset_command` only for an agent kind that is not one of those.

## Troubleshooting

| Symptom | Cause |
| --- | --- |
| `run this from your agent's pane` | The loop was started from a pane with no agent in it. |
| No reviewer found | The workspace has no second agent, or only agents of the author's own kind. Open one, or pin one with `reviewer_name`. |
| The loop stops saying an agent is blocked | That agent is waiting on a question. Answer it, then run the loop again. |
| A review never finishes | Raise `review_timeout` / `fix_timeout`, or cancel with `stop` from any pane. |
| `finish` refuses | A loop still holds the run lock. `stop` it, then finish. |
| History shows `—` for every round | The repository is not a git work tree, or the run's checkpoints have aged out of the retention window. |

Logs live in the plugin state dir and are visible with
`herdr plugin log list --plugin herdr-review-loop`; every verdict is archived under
`history/<run-id>/iteration-NN.md`, beside the run's `run.json` and, once finished, its
`summary.md`.

## Migrating the Node config

Rename `review_timeout_ms` and `fix_timeout_ms` to `review_timeout` and `fix_timeout`,
and convert milliseconds to duration strings. For example, `1800000` becomes `"30m"`.
Unknown legacy keys are ignored with a warning.
