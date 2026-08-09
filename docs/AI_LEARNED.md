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
