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
