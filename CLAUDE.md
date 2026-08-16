# herdr-review-loop

<!-- Keep this file current: when a convention changes, it changes in the same commit. -->

## Project overview

`herdr-review-loop` is a Herdr plugin that drives two coding agents in one workspace against each
other: a reviewer writes structured findings to `.review-loop/run/round-NN/review.json`, the loop
gives each finding an id, the author applies what it agrees with and decides every id in
`decisions.json`, and the loop repeats until the review comes back clean, the round budget runs out,
or an agent gets stuck. Scope is always the uncommitted working tree.

The run directory is hidden from git through `.git/info/exclude`, so nothing the loop writes reaches
the diff under review or a round checkpoint. Everything a run produced is archived under the plugin
state directory, where `report.json` accounts for every finding the run raised.

[docs/SPEC-v2.md](docs/SPEC-v2.md) is normative for the artifact layout, the review contract, the
configuration layers, the archive and the exit codes; [docs/SPEC.md](docs/SPEC.md) still describes
everything v2 does not cover. Every task in SPEC-v2 §12 is built. The one open question is named in
T7.2: the review command table was written against the shipped codex and claude binaries, and a live
pane would still settle whether the in-pane `/review` takes the argument its CLI form documents.

## Build, test and lint

```bash
make build                        # binary into bin/, version taken from herdr-plugin.toml
make test                         # race detector + coverage summary
make lint                         # golangci-lint
make fmt                          # gofmt -s
make prep                         # fmt + vet + lint + test — run before every commit
make install-plugin               # build, then herdr plugin link .
go test -run TestName ./internal/loop/
```

**Never commit without running `make prep`.**

## Architecture

- `cmd/herdr-review-loop/main.go` — `main()` only calls `run([]string) error`. `run` dispatches a
  verb through the `commands` table; each verb is one named function taking an `application`
  (environment, config, client, and `resolve` for the invocation layer). Everything Herdr-facing is
  reached through `herdr.Client`. `configure.go` holds `config` and `init`.
- `internal/herdr` — the Herdr CLI as a Go API. `Client` shells out per call and decodes the JSON
  envelope; `Error` carries Herdr's error code so callers branch with `errors.As`. `Environment`
  decodes the plugin context the host exports; `PickReviewer` chooses the second agent.
- `internal/loop` — the loop itself.
  - `run.go` — `Run.Execute` owns the lifecycle and holds the run lock; `reviewPhase` and `fixPhase`
    are one phase each, and each runs the degradation ladder: parse, one reformat turn, then a
    session reset per remaining retry, then `ExitAgent`.
  - `rundir.go` — `.review-loop/run/`, reached only through an `os.Root` on the repository. Every
    path component is re-checked before each write, because owning a name is not owning what it
    points at and a symlink planted mid-run would redirect the agents' writes too.
  - `findings.go` / `decisions.go` — the review contract. Ids and fingerprints are assigned by the
    loop; `Review.Resolve` is where a status is weighed against its arrays, and it is the only place
    a clean verdict is decided. `Review.Filter` applies `min_verdict` *after* the ids and *before*
    `Resolve`, so a round whose findings were all withheld is clean and says why.
  - `policy.go` / `scope.go` — the round policy and what is under review, each turning configuration
    into the block the reviewer and the author are sent. The last `rounds` entry repeats forever.
  - `adapter.go` — kind → its own review command, plus the capture step that turns what the command
    rendered into the host UI into `review.json`. A kind with no entry gets the built-in prompt,
    which is the only place a review taxonomy of ours remains. Expect this table to rot when an
    agent CLI moves; `review_command` overrides any kind, and `parse_fallback` events are the trail.
  - `archive.go` / `render.go` — the per-run archive and the single renderer `show` and the history
    pane both call.
  - `watchdog.go` — `WatchStall` cancels a phase whose agent has produced no observable output for
    `timeouts.stall`; the stall is a retry, not a terminal failure.
  - `stuck.go` — a fingerprint applied in three consecutive rounds and raised again each time is a
    deadlock, not progress, and ends the run instead of spending the budget.
  - `exit.go` — `ExitError`, so an exit code is chosen where the outcome is known.
  - `checkpoint.go` — snapshots the worktree into refs under `refs/herdr-review-loop/` using a
    temporary `GIT_INDEX_FILE`, leaving the user's index, stash and HEAD untouched. `Tree`/`Diff`
    over the same mechanism produce each round's `changes.patch`, new files included.
  - `lock.go` — `Lock`/`PanelRecord` identify their owner by pid *and* process start time.
- `internal/config` — the settings and the layers they come from. `Resolve` merges built-in defaults
  (`defaults/`, embedded), then the selected profile, then user, project and invocation, per key;
  `rounds` is replaced whole by the layer that defines it. One decoder reads both file kinds,
  because a profile *is* a `config.json` under a name. The file is nested (`reviewer`, `timeouts`,
  `archive`); field keys are the dotted paths, so the settings pane, the loader and the writer share
  one name per setting. A malformed setting produces a warning and the layer below, never a failure:
  `stop` has to work even when `config.json` is broken. `Save` merges into the existing file rather
  than replacing it, so a save from the pane never drops a hand-written `rounds`.
- `internal/ui` — the three panes (panel, settings, history) over a small raw-mode terminal. Each
  falls back to a plain-text dump when its file is not a TTY.
- `bin/*.sh` — the entry points named by `herdr-plugin.toml`.

## Conventions

- `main()` only parses arguments and calls `run() error`. All work happens below it.
- Every blocking function takes `ctx context.Context` first. Never store a context in a struct.
- Every goroutine has an owner who waits for it, and the wait is bounded by a timeout.
- Interfaces are declared on the consumer side and kept small — `loop.AgentClient` and `loop.Client`
  name only the Herdr calls the loop actually makes.
- Wrap errors: `fmt.Errorf("failed to X: %w", err)`. Compare with `errors.Is` / `errors.As`. Errors
  crossing into our own packages already name what failed; stdlib errors must be given context.
- No `else if` chains, no `else` where an early return works, no `goto`, no `init()`.
- Comments lowercase except godoc. Comments say *why*, and describe current intent, never history.
- Every exported identifier has a godoc comment. `revive` enforces this.
- US spelling everywhere, including user-facing strings — `canceled`, not `cancelled`. `misspell`
  enforces this, and it keeps us consistent with `context.Canceled`.
- Files the plugin owns are created `0o600`, directories `0o700`.
- Tests: standard library only — `testing`, table-driven subtests, no assertion framework. No
  `time.Sleep` for synchronisation. Temporary state goes in `t.TempDir()`.

## Dependencies

- Prefer the standard library. Every new dependency needs a one-sentence justification. The only
  ones today are `golang.org/x/sys` (flock, poll) and `golang.org/x/term` (raw mode).
- Keep the Go version identical in `go.mod` and `.github/workflows/ci.yml`.
- The plugin version lives in `herdr-plugin.toml` and nowhere else: `bin/ensure-binary.sh` rejects a
  binary whose `version` output disagrees with it.
- Dependabot is notification-only; update in deliberate batches and run `make prep` afterwards.
