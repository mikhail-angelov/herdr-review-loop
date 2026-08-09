# herdr-review-loop

[![CI](https://github.com/mikhail-angelov/herdr-review-loop/actions/workflows/ci.yml/badge.svg)](https://github.com/mikhail-angelov/herdr-review-loop/actions/workflows/ci.yml)

`herdr-review-loop` lets the agent in the current Herdr pane and an agent of a
different kind in the same workspace review one another in a bounded loop.

## Requirements

Herdr 0.7.5 or newer on macOS or Linux. Installed releases include a matching
binary, so Go is needed only to build an unreleased checkout.

## Install

```sh
herdr plugin install mikhail-angelov/herdr-review-loop
herdr server reload-config
```

The plugin downloads a matching release binary when one exists; otherwise a Go
1.26 toolchain is needed to build an unreleased checkout. For development:

```sh
make build
herdr plugin link .
```

## Commands and review contract

| Command | What it does |
| --- | --- |
| `review` | Runs the reviewer → author → reviewer loop. It exits 0 only when the review is clean. |
| `review --dry-run` | Prints the author and reviewer without taking a lock or writing files. |
| `stop` | Cancels the active loop from any pane. |
| `open-panel` / `open-settings` | Opens the corresponding pane. |

The reviewer writes `review.md` by default. Its first non-empty line must be
either `STATUS: CLEAN` or `STATUS: FINDINGS`; any other result is treated as
findings so the loop errs toward another review. The author must then rewrite
`review-summary.md` with the compact record of applied, rejected, or deferred
decisions. The loop stops if the author does not update that file.

Both sessions are reset before their turns. The reviewer reads the current
uncommitted diff and `review-summary.md`; the author reads the review, summary,
and diff. Round 1 is a normal review, round 2 verifies remaining high/medium
issues, and later rounds report only regressions or high-severity findings.
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

The panel uses `r` to start a review, `x` to cancel it, `s` for settings, and
`q` to close. Settings use `j`/`k` or arrows to select, Enter to edit, `d` to
restore a default, `s` to save, `x` to cancel the loop, and `q` to close.
`stop` is safe from any pane. The panel is a detached process, so closing it
never cancels a review in progress.

## Configuration

Configuration is stored in Herdr's plugin config directory as `config.json`.
Durations use Go notation such as `30m` or `90s`.

| Key | Default | Meaning |
| --- | --- | --- |
| `reviewer_kind` | empty | Reviewer agent kind; empty chooses a different kind. |
| `reviewer_name` | empty | Reviewer name or pane id; overrides `reviewer_kind`. |
| `max_iterations` | `10` | Maximum review rounds. |
| `review_file` | `review.md` | Reviewer output, relative to the repository. |
| `review_timeout` | `30m` | Budget for one review phase. |
| `fix_timeout` | `30m` | Budget for one author fix phase. |
| `reset_command` | empty | Reset command for agent kinds without a built-in command. |

Invalid values and unknown keys are ignored with a warning; the loop uses the
default instead. The settings pane validates values before saving them.

## Migrating the Node config

Rename `review_timeout_ms` and `fix_timeout_ms` to `review_timeout` and
`fix_timeout`, and convert milliseconds to duration strings. For example,
`1800000` becomes `"30m"`. Unknown legacy keys are ignored with a warning.
