## 2026-08-09 — Raw terminal frames need CRLF

### Goal

Render multi-line Go TUIs without diagonal, stair-stepped rows.

### Golden path

1. Enter raw mode with `term.MakeRaw` for key input.
2. Before writing a complete frame, replace every `\n` with `\r\n`.
3. Clear and home the cursor, then write the normalized frame.

### Verification

`go test ./internal/ui` verifies that `Terminal.Frame("first\nsecond")` writes `first\r\nsecond`; `go test ./...` passes.

### Failure pattern avoided

`term.MakeRaw` disables output post-processing. A bare line feed moves the cursor down without returning it to column zero, so each rendered line starts farther right than the previous one.

### Ruled-out approaches

- Tried resizing the popup renderer to the manifest percentages; it did not address the diagonal rows.
- Tried changing popup placement handling; it did not address the diagonal rows.

## 2026-08-09 — Raw TUI shortcuts need UTF-8 layout handling

### Goal

Keep physical TUI shortcuts usable on macOS with a Russian keyboard layout.

### Golden path

1. Decode non-ASCII terminal input as a complete UTF-8 rune before dispatching it.
2. Outside text-editing modes, map Russian physical-key equivalents (`й/ы/к/ч/в/о/л`) to `q/s/r/x/d/j/k`.
3. Leave input unchanged while editing text fields.

### Verification

`go test ./internal/ui` verifies complete UTF-8 key reads and the Russian-layout mappings; `go test ./...` passes.

### Failure pattern avoided

A byte-at-a-time key reader interprets `й` as the first UTF-8 byte (`Ð`), so neither a Russian shortcut nor Unicode text input reaches the intended handler.

### Ruled-out approaches

- Tried treating every input byte as a standalone key; it broke multi-byte UTF-8 characters.

## 2026-08-09 — Review verdict timestamps must use filesystem precision

### Goal

Accept a verdict file written after the review prompt on filesystems that round modification times to seconds.

### Golden path

1. Capture `askedAt` before submitting the review prompt.
2. Compare the verdict file's modification time with `askedAt` truncated to `time.Second`.
3. Keep removing the prior verdict after the reviewer settles and before prompting, so the timestamp check is not the sole stale-file protection.

### Verification

`TestWrittenSinceAllowsRoundedFileTimestamp` creates a verdict with a second-rounded timestamp and accepts it; `go test ./...` and `go vet ./...` pass.

### Failure pattern avoided

Comparing a second-rounded filesystem `mtime` with nanosecond-precision `time.Now()` marks a newly written `review.md` as stale. The loop then waits 15 seconds and fails with `reviewer settled without writing review.md`.

### Ruled-out approaches

- Tried a strict `mtime >= askedAt` comparison; it rejects a verdict whose timestamp is rounded to the same second as the prompt.

## 2026-08-09 — Recover Go tests from a full temporary volume

### Goal

Restore Go test runs when `t.TempDir` fails with `no space left on device`.

### Golden path

1. Confirm the failure is storage-related with `df -h <temporary-directory>`.
2. Check the Go build-cache size with `du -sh "$(go env GOCACHE)"`.
3. Run `go clean -cache` and rerun `go test ./...`.

### Verification

With only 121 MiB free, `go test ./...` could not create test temporary directories. Clearing a 526 MiB Go build-cache increased free space to 653 MiB; `go test ./...` and `go vet ./...` then passed.

### Failure pattern avoided

Repeated test runs fail before exercising code because `testing.T.TempDir` cannot create its temporary directory.

### Ruled-out approaches

- Retried the full test suite without freeing space; it failed again while creating `TempDir`.

### Notes

`go clean -cache` removes rebuildable artifacts only; it does not modify repository files.

## 2026-08-09 — Herdr void commands return empty stdout

### Goal

Allow review-loop commands that inject text or report display metadata to succeed with the current Herdr CLI.

### Golden path

1. Inspect the plugin log when a reset command appears in a pane but the review does not advance.
2. For Herdr commands with no useful response (`agent send-keys`, `pane send-text`, `pane send-keys`, and `workspace report-metadata`), use `Client.void`: empty successful stdout is accepted, while JSON error envelopes from stdout or stderr are decoded and returned.
3. Keep `Client.Call` for commands whose result is parsed as JSON.

### Verification

`TestVoidCommandsAcceptEmptySuccessfulOutput` first failed because each command returned `invalid JSON` for a successful empty response, then passed with `Client.void`; it also verifies JSON error envelopes on stdout. `go test ./...` and `go vet ./...` passed.

### Failure pattern avoided

`pane send-text` inserts `/clear`, but treating its empty successful response as an error stops the loop before it sends Enter; the command remains in Claude's composer and no review prompt is sent.

### Ruled-out approaches

- Tried using `Client.Call` for void commands; it rejected the empty successful response as invalid JSON.
- Tried using `Client.Text` for void commands; it treated a JSON error envelope on successful stdout as ordinary text and swallowed the command failure.

## 2026-08-16 — Archive every retry and reformat turn separately

### Goal

Keep a review run diagnosable when a phase needs a reformat turn or retry.

### Golden path

1. Store the first phase turn under the stable primary names, such as `prompt-review.md` and `review.raw.txt`.
2. Store reformat and retry turns under unique, attempt-numbered names, preserving `.raw.txt` as one extension.
3. Archive each raw response before issuing a reformat or resetting for a retry.

### Verification

`TestArchiveKeepsRetryAndReformatTurnsSeparate` and `TestRunSpendsOneReformatTurnOnGarbledOutput` pass. `golangci-lint run`, `go vet ./...`, `go test -race ./...`, and `make build` pass.

### Failure pattern avoided

Writing every turn to `prompt-review.md` or `review.raw.txt` overwrites evidence from a failed attempt, leaving an archive unable to explain why a later retry occurred.

### Ruled-out approaches

- Tried reusing the primary archive filenames for every attempt; later prompt and raw writes replaced earlier evidence.

## 2026-08-16 — Configuration layers need one decoder and one non-default writer

### Goal

Resolve settings across built-in defaults, a profile, the user, the project and the invocation
without a save from the settings pane destroying what only a file can express.

### Golden path

1. Decode every layer with one function into flattened dotted keys, so `config.json` and a profile
   are the same file kind under different names.
2. Merge per key, lowest layer first, recording the winning layer for each so `config` can print it.
3. Replace arrays such as `rounds` whole; never merge them element-wise.
4. Have the settings pane's `Save` read the existing file and overlay only the scalar keys it owns,
   deleting the ones back at their default.

### Verification

`TestOneDecoderReadsBothFileKinds`, `TestAProjectProfileReplacesRoundsWhole`,
`TestSaveKeepsStructuredSettingsThePaneCannotEdit` and `TestInitProducesAProjectLayerThatChangesNoBehavior`
pass. `golangci-lint run`, `go vet ./...`, `go test -race ./...` and `make build` pass.

### Failure pattern avoided

`Save` rebuilding `config.json` from the pane's field list drops every key the pane cannot edit, so
saving one timeout silently deletes a project's committed round policy.

### Ruled-out approaches

- Tried keeping `Values` comparable with `==` for the pane's dirty check; a map and a slice put the
  struct out of reach of it, so the pane compares over the field list with `config.Same`.
- Tried placing the profile above the user's `config.json`; a built-in default profile then
  overrode settings the user had written by hand.

## 2026-08-16 — A stall is a retry, a cancellation is not

### Goal

Catch an agent that goes silent within `timeouts.stall` rather than at the end of the phase budget,
without turning an ordinary cancellation into a reported stall.

### Golden path

1. Run the prompt inside `WatchStall`, which polls the agent's state sequence and pane content and
   cancels a derived context once neither has changed for the budget.
2. Join the watcher before reporting, bounded by its own timeout.
3. Report a stall only when the watchdog fired *and* the parent context is still alive; otherwise
   return the original error, so cancellation and an exhausted phase budget stay what they are.
4. Return the stall as a plain error, not a `context.Canceled`, so the phase's retry ladder treats
   it as an attempt failure rather than a terminal one.

### Verification

`TestWatchStallFiresOnSilenceAndNotOnProgress` and
`TestASilentAgentIsCaughtByTheStallBudgetNotThePhaseBudget` pass: a 30-minute phase with a
150ms stall budget ends in under a second, having made `retries + 1` attempts.

### Failure pattern avoided

Letting the stall cancellation surface as `context.Canceled` makes `terminal(err)` true, so the run
exits 4 as if the user had stopped it and no retry is ever attempted.

## 2026-08-16 — Verify a CLI adapter against the shipped binary, not from memory

### Goal

Write the agent-kind → review-command table without a live pane to confirm each command in.

### Golden path

1. Confirm the installed versions first (`codex --version`, `claude --version`) and check them
   against whatever the specification claims was verified.
2. Read the command surface out of the shipped binary — `codex review --help` for the CLI form,
   `strings` over the platform binary for the in-pane slash commands and their prompt templates.
3. Encode only what the binary shows, and write down in the adapter comment the one detail that
   still needs a person.
4. Design the fallback so an unconfirmed detail degrades rather than breaks: an ignored instruction
   argument still leaves the review running at the agent's own default scope.

### Verification

`codex review --help` names `--uncommitted`, `--base`, `--commit` and `[PROMPT]` with no level flag;
the codex TUI binary lists `/review - review any changes and find issues` and the prompt template
"Review the current code changes (staged, unstaged, and untracked files)"; the Claude Code binary
carries `/code-review`, `/security-review`, and the rule that `ultra` is user-triggered and billed.
`TestANativeReviewerRunsItsOwnCommandThenRecordsIt` covers codex, claude and a kind with no command.

### Failure pattern avoided

Both native commands render findings into the host UI rather than to a file, so an adapter that
sends the command and then reads `review.json` finds nothing. The capture step is a second turn, not
an option.

A slash command also occupies its whole line, so a kind whose argument slot is already its level has
nowhere to put the round's instructions — sending them appended to `/code-review high` would send
the level and drop the instructions silently. Those kinds get a preamble turn instead.

### Ruled-out approaches

- Tried sending codex's instructions as a preamble turn like claude's; codex's `/review` runs in a
  separate review thread, so the main thread's preceding turn is not reliably in its context. Its
  instructions go in the command argument, which is what its CLI form documents.
