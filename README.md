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
