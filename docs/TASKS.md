# herdr-review-loop (Go) — Task plan

Implementation tasks for [SPEC.md](SPEC.md). 15 tasks in 5 phases, ordered by dependency.
Each lists what it produces and how it is judged done.

Legend: **§** references a SPEC section · *deps* are task ids.

---

## Phase 0 — Skeleton and install path

### T0.1 — Bootstrap `chore`
`go.mod` (`github.com/mikhail-angelov/herdr-review-loop`, `go 1.26.0`), `.gitignore`
(`bin/herdr-review-loop`, `dist/`, `run.lock`, `herdr-review-loop.log`, `history/`, `panel.*.json`), MIT
`LICENSE`, `Makefile` (`build` · `test` · `tidy` · `install-plugin`), `.golangci.yml`
(v2 defaults + the `SA5011`-in-tests exclusion).
**Done:** `make test` runs on an empty tree; `golangci-lint run` is clean.

### T0.2 — CI `ci`
*deps: T0.1* — `.github/workflows/ci.yml`, `permissions: contents: read`: matrix
`ubuntu-latest`/`macos-latest` running `go vet ./...`, `go build ./...`, `go test ./...`
and `gofmt -l .` — **the `./...` is required**, since no `.go` file sits in the repository
root and the bare forms exit 1 with `no Go files in …`; plus one Linux `golangci-lint` job
(`golangci/golangci-lint-action@v9`, `version: latest`). Go `1.26`, patch-floating. §7.4
**Done:** the first pushed commit is green on both runners, golangci-lint included, and
each Go step is verified to actually compile/test the packages rather than exit early.

### T0.3 — Binary provisioning `build`
*deps: T0.1* — `bin/run.sh` (`$HERDR_REVIEW_LOOP_BIN` → `bin/herdr-review-loop` → `PATH`,
else exit 127 with the fix), `bin/run-panel.sh`, `bin/run-settings.sh`, and
`bin/ensure-binary.sh` implementing §7.2, in this order: the acceptance test (the whole
trimmed stdout of `bin/herdr-review-loop version` equals the manifest `VERSION` — a binary
reporting anything else is stale and replaced) → download the asset **and `SHA256SUMS`**
for the exact tag `v$VERSION`, verify **only this asset's line** with whichever verifier
the host has (`sha256sum -c -` on Linux, `shasum -a 256 -c -` on macOS — neither exists on
both; no verifier at all means fall through, never install unverified), since the file
lists all four platforms and checking it whole fails on the three that were not
downloaded, `chmod +x`, acceptance test →
`go build -o bin/herdr-review-loop -ldflags "-X main.version=$VERSION" ./cmd/herdr-review-loop`,
acceptance test → fail naming both fixes. **No `latest` fallback.** Modes `--in-tree` /
`--build` / default.
**Done:** with the binary deleted and `go` hidden from `PATH`, `--in-tree` installs the
released asset — verified against a real release whose `SHA256SUMS` lists all four
platforms, so a whole-file check would fail here and an extracted-line check passes; a
tampered asset (checksum mismatch) and a missing `SHA256SUMS` are both
rejected rather than installed; a binary left over from an older `VERSION` is replaced,
not accepted; with a Go toolchain and a manifest version that has no release it compiles
to `bin/` and the launcher resolves it; without either, the failure names both fixes and
installs nothing.

### T0.4 — Manifest `feat`
*deps: T0.3* — `herdr-plugin.toml`: metadata, `[[build]]` hook, five actions (`review`,
`pair`, `stop`, `panel`, `settings`), two panes (`panel` split, `settings` popup
72 %×60 %). §7.1
**Done:** `herdr plugin link .` succeeds; `herdr plugin action list --plugin herdr-review-loop`
lists all five.

### T0.5 — Release workflow `ci`
*deps: T0.2, **T0.3*** — the download path in T0.3 is this task's only consumer, and the
asset names, the `SHA256SUMS` layout and the injected version are one contract between
them; landing the producer first would leave nothing to validate it against.
`.github/workflows/release.yml`: `permissions: contents: write`, verify (the same four
checks **with `./...`**) → cross-compile `darwin/{arm64,amd64}` + `linux/{amd64,arm64}`
into `dist/` with `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=${GITHUB_REF_NAME#v}"`
→ `(cd dist && sha256sum herdr-review-loop-* > SHA256SUMS)` so the lines carry **bare**
asset names → `gh release create "$GITHUB_REF_NAME" dist/* --title "$GITHUB_REF_NAME"
--generate-notes` with `GH_TOKEN: ${{ github.token }}`. §7.4
**Done:** a `v0.0.1-test` tag publishes four assets **and** `SHA256SUMS` (not an empty
release); every line of `SHA256SUMS` names the asset without a `dist/` prefix; each
binary's `version` prints the tag without its `v`; and T0.3's `--in-tree` installs the one
for this platform end to end.

> Phases 0.3/0.5 land early on purpose: the install story is the point of the rewrite, and
> a broken asset-name contract is cheapest to find before there is code to install.

---

## Phase 1 — Foundations

### T1.1 — `internal/herdr` client and env `feat`
`env.go`: typed `HERDR_PLUGIN_CONTEXT_JSON` (workspace id/cwd, focused pane id/cwd),
state dir, config dir, binary path, each with its documented fallback (§5.1).
`client.go`: `Call(ctx, args…)` decoding the `{result}` / `{error:{code,message}}`
envelope from stdout or stderr, `Text(ctx, …)` for `pane read`, and typed helpers for
every command the plugin issues (`AgentList/Get/Wait/Prompt/Focus/SendKeys`,
`PaneSendText/SendKeys/Read/Layout/Resize`, `PluginPaneOpen/Focus`,
`WorkspaceReportMetadata`, `NotificationShow`). Errors carry the herdr `code` so callers
match `agent_prompt_stalled`. Every call context-cancellable (§9.2).
**Done:** envelope tests for success, structured error, non-JSON stderr, non-zero exit,
cancelled context; env tests for missing/malformed values.

### T1.2 — `internal/herdr` agents `feat`
*deps: T1.1* — `List`, `Find(paneID)`, `PickReviewer(cfg, all, author, note)` with the
name → kind → cross-review precedence and its error messages, `Target`, `Describe`. §5.2
**Done:** tests for every branch, including pane-id-as-name, wrong workspace, zero
candidates, multiple candidates (first wins + note).

### T1.3 — `internal/config` `feat`
Field registry (key, label, kind, default, hint, limits); `Load` (defaults + file,
warnings for rejected values and unknown keys, non-object file → defaults + one warning);
`Save` (non-defaults only, temp file + rename); `Parse` (text → value, for the settings
pane); `Show` (value → text). Durations via `time.ParseDuration`. §6
**Done:** table tests for every field × (valid, invalid, empty, out of range); round-trip
stores only what differs from defaults.

---

## Phase 2 — The loop

### T2.1 — Review file and log `feat`
*deps: T1.3* — `loop/review.go`: an `*os.Root` opened on the repository through which
**every** review-file operation runs (`Remove`, `Lstat`, `ReadFile`, parent `MkdirAll`),
so containment is enforced per call rather than validated once and trusted; plus the
startup resolve-and-compare check for a readable early error, the regular-file check,
`WrittenSince`, and `ParseVerdict` on the first non-empty line (§5.4). `loop/log.go`:
`[RFC3339] message` appended to
`<state-dir>/herdr-review-loop.log` and mirrored to stdout, `Tail` over the last 8 KiB, `Phase()`,
`LastOutcome()`, `Archive(runID, i, text)` (§5.10).
**Done:** containment tests for `../`, absolute, symlinked subdirectory, directory at the
path; **a post-validation swap — validate `sub/review.md`, then replace `sub` with a
symlink to a directory outside the repository, then delete: the file outside must survive
and the operation must fail**; STATUS matrix; log tail beyond the window and with no
matching lines.

### T2.2 — Run lock `feat`
*deps: T1.1* — `loop/lock.go`: `unix.Flock` exclusive lock plus a sidecar record —
pid, workspace, started, **and the pid's start time as `ps -p <pid> -o lstart=` reports
it**, which is what makes the pid checkable later (§5.9) — published **atomically
(temp file + rename) immediately after the lock is acquired, before any other work**, so a
concurrent `stop` sees a whole record or none, plus the reader-side retry (~1 s) that fails
without signalling when it still cannot validate one;
`Acquire` (refuse and name the holder), `Release`, `Holder()`, `StillTheHolder(rec)`
re-reading that start time; per-workspace panel record with exclusive-create claim, stale
clearing and release — the panel record carries pid **and start time** too, and counts as
live only when both match (§5.7), since a recycled pid there leaves a workspace unable to
open any panel at all. §5.7, §5.9, §9.1
**Done:** a second process is refused while the lock is held; the lock is free after the
holder is SIGKILLed; `StillTheHolder` is false for a pid that exited and false for a
recycled pid (simulated by a record carrying a different start time); **a held lock with
no record, and one with a truncated record, both make `stop` retry and then fail without
sending a signal, rather than treat the missing data as licence to guess a pid**; a panel
record whose
start time does not match the live pid is treated as stale and cleared rather than
deferred to; two concurrent panel claims produce exactly one winner.

### T2.3 — Driving an agent `feat`
*deps: T1.2* — `loop/agent.go`, every entry point taking the **phase `context.Context`**
(created once per phase with `context.WithTimeout`) and deriving each call's `--timeout`
from the time **remaining** on it, never from the configured budget — one deadline shared
by the phase, so a round cannot spend it several times over (§5.6 *Deadlines*):
`Settle` (get → wait when working → fail when blocked); `SubmitAndWait`
with the `agent_prompt_stalled` recovery (state-sequence advance check, up to 3 enter
presses with 4 s checks, then `agent wait`) (§5.6); `ResetSession` — the
built-in command per kind, the `reset_command` fallback for unknown kinds, and the type-verify-retry dance
(`send-text` → 400 ms → last 6 non-empty visible lines → `enter` + 1500 ms; else `esc` +
retry, 3 attempts) (§5.5).
**Done:** fake-client tests for prompt accepted, stalled-but-advanced, stalled-then-enter
#2, nothing works → error, blocked → focus + error; reset on attempt 1, attempt 2, three
failures, unknown kind, configured override. The fake records the `--timeout` of every
call and asserts it **decreases** across a phase and never exceeds what is left, so a
budget handed out afresh per call fails a test rather than quietly authorising several
times the configured wait; a phase whose deadline expires mid-call kills the child process
(`exec.CommandContext`) instead of waiting for herdr to return, and one already at zero
fails without issuing a call at all.

### T2.4 — Prompts and the run `feat`
*deps: T2.1, T2.2, T2.3* — `loop/prompts.go` rendering **Appendix A** verbatim (round-1 vs
later-round opening, fix prompt) with checked-in golden fixtures, and `loop/run.go`
orchestrating §5.3: delete-after-settle ordering, `askedAt` freshness, per-round
archiving, progress tokens, notifications on every exit path, focus back to the author,
`review_timeout` for the review phase and `fix_timeout` for the apply phase, and the
dry-run path that takes no lock and writes nothing.
**Done:** golden tests for both prompts at rounds 1 and 3 match Appendix A character for
character; fake-client tests for clean-on-round-1, findings→fix→clean, budget spent
(exit 1), blocked agent, reviewer that never writes the file, dry run, and a run where the
two phases are given different budgets and each phase's calls draw down its own deadline —
a review round that settles, waits, prompts and recovers a stalled prompt must not exceed
`review_timeout` in total.

### T2.5 — Cancellation `feat`
*deps: T2.4* — Loop context cancelled on SIGTERM/SIGINT; on cancel it clears the progress
token, releases the lock, logs `cancelled`, exits non-zero. `herdr-review-loop stop`: read
the holder, **re-verify the pid's start time against the record immediately before each
signal** and skip the signal when it no longer matches (§5.9), SIGTERM, 3 s, re-verify,
SIGKILL, 1 s, clear the **loop's** workspace token, notify, exit 1 only if it survived.
§5.9, §9.2
**Done:** an integration test runs `herdr-review-loop review` against a stub `herdr` on
`PATH`, cancels it, and asserts the lock is free, the token cleared and the log line
written; a record whose start time does not match the live pid results in no signal being
sent at all.

> The plugin is usable from here: `herdr plugin link .` plus the `review`, `pair` and
> `stop` actions, driven from a keybinding. Panes come next.

---

## Phase 3 — Panes

### T3.0 — Terminal plumbing `feat`
`ui/tui.go`, implemented on `golang.org/x/term`: raw mode on start and restore
on exit, hide/show cursor, full-frame write, SIGWINCH-driven re-render, and the key
decoder — a chunk split into keys, whole escape sequences kept intact
(`ESC [ … @-~` and `ESC O @-~`), an unfinished trailing sequence held for 40 ms for the
rest of itself and then delivered as literal keys. `ui/style.go`: bold / dim / red /
invert / clip. §3.1, §9.3
**Done:** decoder tests for a CSI sequence split across two reads, a bracketed-paste
marker not becoming five keys (the first of which would mean quit), application-cursor
arrows, a burst of keys in one read, and a lone `esc` delivered after the window. Raw
mode is restored on every exit path, including panic.

### T3.1 — Panel `feat`
*deps: T3.0, T2.1, T2.2, T1.2* — `ui/panel.go`: 1 s render tick, 5 s pair refresh,
header / pair / phase / log tail / message / hints, clipping and hint wrapping,
`r x s q` keys, `r` detaching the loop with `HERDR_REVIEW_LOOP_AUTHOR` in the overridden context,
`x`/`s` re-invoking this binary. Panel claim with focus-the-winner-and-exit. Rendering is
a pure function of (state, width, rows) → string, so views are testable without a
terminal.
`loop/panel.go`: opening via `plugin pane open … --no-focus`, then `pane layout` →
target width → `pane resize`; every failure logged, never fatal. §5.7
**Done:** view tests at 32/44/80 columns; width tests at 40/64/100/200; fake-client tests
for resize direction and amount; manual check that closing the panel does not cancel a run.

### T3.2 — Settings `feat`
*deps: T3.0, T1.3, T2.2* — `ui/settings.go` over the field registry:
navigation, edit/accept/cancel, `d` default, `s` save, `x` cancel run, `q` with the
unsaved-changes confirmation, dimmed defaults, inline validation errors, running/idle
header, and the non-TTY text dump. §5.8
**Done:** model tests for edit→reject→fix→save and quit-confirm; adding a field to the
registry changes the pane with no edit to `ui`.

---

## Phase 4 — Wiring and release

### T4.1 — `cmd/herdr-review-loop` `feat`
*deps: Phase 2, Phase 3* — Subcommand table per §4, `version` from the ldflags variable,
`help`, unknown → usage + exit 2, documented exit codes.
**Done:** every subcommand reachable; a release build's `herdr-review-loop version` prints the tag,
which is what `ensure-binary.sh` smoke-tests.

### T4.2 — README and v0.1.0 `docs`
*deps: T4.1, T0.5* — README: badges, what it does, requirements, install (§7.3),
keybinding snippets, the sessions/reset rationale, panel, cancellation, settings, the
STATUS termination contract, the config table, the Node `config.json` migration, and the
development install. Then bump the manifest to `0.1.0`, tag, and verify the whole install
path end to end on a machine without Go (DoD §10.3).
**Done:** a reader with no context installs and binds keys from the README alone.

### T4.3 — Retire the reference snapshot `chore` ✓
*deps: T4.2* — The porting snapshot and all pointers to it have been removed. Settings
field labels and hints, user-facing error and cancellation wording, and key-decoder rules
are now maintained in Go code or SPEC §5/§6/Appendix A.
**Done:** the tree builds and tests pass without any snapshot dependency; the predecessor
repository can be deleted without this one losing anything.

---

## Order

```
T0.1 ─┬→ T0.2 ─┐
      └→ T0.3 ─┴→ T0.5
            └→ T0.4
T1.1 → T1.2 ┐
T1.3 ───────┴→ T2.1 → T2.2 → T2.3 → T2.4 → T2.5
                                              ↓
                                      T3.0 → T3.1
                                          └→ T3.2
                                              ↓
                                      T4.1 → T4.2 → T4.3
```

---

## Risks

| Risk | Mitigation |
| --- | --- |
| The reset-by-typing dance is timing-sensitive and differs per agent TUI | Preserve the constants (400 ms / 3 attempts / 6 lines / 1500 ms) exactly; verify against live claude and codex panes before v0.1.0 (T2.3, DoD §10.2) |
| `agent_prompt_stalled` recovery depends on `state_change_seq` semantics | Fake-client tests first, then confirm against live agents; keep the enter-press fallback |
| Hand-rolled terminal handling misbehaves in some terminal Herdr hosts | The code being ported already runs in these exact panes; T3.0 lands first, standalone and test-covered, so a problem shows up before two panes are built on it. bubbletea remains the escape hatch (§9.3) at a measured +32 % binary |
| Asset names / manifest version / download logic drift apart | One contract, three files (§7.4); the `herdr-review-loop version` smoke test in `ensure-binary.sh` catches a mismatch at install time rather than at first use |
| `flock` semantics on unusual filesystems (NFS, some containers) | State dir is local under `~/.config`/`~/.local`; document the assumption and fail loudly rather than silently run two loops |
