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

Not sure who would be paired? `review --dry-run` prints the pair and touches nothing.

For development instead of install:

```sh
make build
herdr plugin link .
```

## Commands

| Command | What it does |
| --- | --- |
| `review` | Runs the reviewer → author → reviewer loop. Exits 0 only when the review is clean. |
| `review --dry-run` | Prints the author and reviewer without taking a lock or writing files. |
| `stop` | Cancels the active loop from any pane. |
| `open-panel` / `open-settings` | Opens the corresponding pane. |

## The review contract

The reviewer writes `review.md` by default. Its first non-empty line must be either
`STATUS: CLEAN` or `STATUS: FINDINGS`; any other result is treated as findings, so the
loop errs toward another review. Findings are one per bullet:

```
- [high|medium|low] path/to/file.ext:LINE — what is wrong — what to do about it
```

The author must then rewrite `review-summary.md` with the compact record of applied,
rejected, or deferred decisions. The loop stops if the author does not update that file —
a round that changes nothing and records nothing is a round that would repeat forever.

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
```

The panel uses `r` to start a review, `x` to cancel it, `s` for settings, and `q` to
close. Settings use `j`/`k` or arrows to select, Enter to edit, `d` to restore a default,
`s` to save, `x` to cancel the loop, and `q` to close. `stop` is safe from any pane. The
panel is a detached process, so closing it never cancels a review in progress.

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

Logs live in the plugin state dir and are visible with
`herdr plugin log list --plugin herdr-review-loop`; every verdict is archived under
`history/<run-id>/iteration-NN.md`.

## Migrating the Node config

Rename `review_timeout_ms` and `fix_timeout_ms` to `review_timeout` and `fix_timeout`,
and convert milliseconds to duration strings. For example, `1800000` becomes `"30m"`.
Unknown legacy keys are ignored with a warning.
