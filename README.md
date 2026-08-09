# herdr-review-loop

`herdr-review-loop` lets the agent in the current Herdr pane and an agent of a
different kind in the same workspace review one another in a bounded loop.

## Install

```sh
herdr plugin install mikhail-angelov/herdr-review-loop
```

The plugin downloads a matching release binary when one exists; otherwise a Go
1.26 toolchain is needed to build an unreleased checkout. For development:

```sh
make build
herdr plugin link .
```

## Commands

`review` runs the loop, and `review --dry-run` prints the selected author and
reviewer without changing anything. `stop` cancels the active loop. The panel
and settings actions open their corresponding panes.

The reviewer writes `review.md` by default. Its first non-empty line must be
either `STATUS: CLEAN` or `STATUS: FINDINGS`; any other result is treated as
findings so the loop errs toward another review.

Configuration is stored in Herdr's plugin config directory as `config.json`.
Supported keys are `reviewer_kind`, `reviewer_name`, `max_iterations`,
`review_file`, `review_timeout`, `fix_timeout`, `reset_command`, and `scope`.
Durations use Go notation such as `30m` or `90s`.

## Keybindings and panes

Bind the actions in your Herdr keybindings, for example:

```toml
# Replace the key chords with your preferred bindings.
[[keybindings]]
key = "ctrl-r"
action = "herdr-review-loop.review"
```

The panel uses `r` to start a review, `x` to cancel it, `s` for settings, and
`q` to close. Settings use `j`/`k` or arrows to select, Enter to edit, `d` to
restore a default, `s` to save, and `q` to close. `stop` is safe from any pane.

## Migrating the Node config

Rename `review_timeout_ms` and `fix_timeout_ms` to `review_timeout` and
`fix_timeout`, and convert milliseconds to duration strings. For example,
`1800000` becomes `"30m"`. Unknown legacy keys are ignored with a warning.
