# herdr-review-loop (Go) — Specification

A rewrite of the Node.js Herdr plugin `hreview` as a single Go binary, installable
without a Go toolchain. The plugin is renamed to match this repository: everything ships
from `herdr-review-loop`.

The predecessor repository is being retired, so nothing here depends on it. The prompts,
the only part of it that is normative, are reproduced in **Appendix A**.

- **Repository:** `github.com/mikhail-angelov/herdr-review-loop`
- **Go module:** `github.com/mikhail-angelov/herdr-review-loop`
- **Binary / plugin id:** `herdr-review-loop`
- **Requires:** Herdr ≥ 0.7.5, macOS or Linux. Go ≥ 1.26 **only to build**.

Packaging follows [senna-lang/herdr-agent-usage](https://github.com/senna-lang/herdr-agent-usage)
where it earns its keep — the `[[build]]` hook, the release assets, CI — and deliberately
does not follow it elsewhere (§9.6).

---

## 1. What the plugin does

Two agents in the same Herdr workspace cross-review each other. The **author** is the
agent whose pane the action is invoked from; the **reviewer** is an agent of a
*different kind* in the same workspace (claude ↔ codex, and so on).

One run alternates between them until the review is clean or the iteration budget is
spent:

```
reviewer (session wiped) → writes review.md
        → author applies the findings it agrees with
        → reviewer (session wiped) re-reviews
        → …  clean | budget spent | agent blocked
```

A narrow **panel** pane opens beside the author showing the pair, the current round and
the log; a **settings** popup edits the run's configuration. Both are TUIs.

The behaviour below is the contract. Where the Go version deliberately differs from
today's Node implementation, it is called out in §9.

---

## 2. Goals / non-goals

**Goals**

1. Behavioural parity with the Node plugin for the loop, the panel and the settings
   pane — the hard-won details in §5 are requirements, not implementation trivia.
2. One statically linked binary; **no Node, and no Go on the user's machine**.
3. `herdr plugin install mikhail-angelov/herdr-review-loop` as the whole install story.
4. Tests over the pure cores, with the herdr CLI behind an interface so the loop is
   testable without a live server.
5. The smallest amount of repository machinery that delivers 2–4. Anything that only
   pays off for a multi-maintainer, multi-provider project stays out (§9.6).

**Non-goals**

- Backward compatibility with the Node plugin's `config.json` (durations change shape,
  §6). Migration is a documented rename, not code.
- Reviewing across workspaces, or more than two agents in a loop.
- Windows support (Herdr is macOS/Linux; the manifest declares the same).
- A general-purpose herdr Go SDK. `internal/herdr` covers only what this plugin calls.

---

## 3. Repository layout

Four internal packages, split where there is a real seam (external process, persisted
format, orchestration, terminal), not one per file:

```
cmd/herdr-review-loop/main.go   subcommand dispatch, version, usage
internal/herdr/                 everything that touches the herdr CLI:
    client.go                     typed calls + JSON envelope + error codes
    env.go                        HERDR_* context, state dir, config dir, binary
    agents.go                     list / find / pick reviewer / target / describe
internal/config/                field registry, defaults, load, save, parse, display
internal/loop/                  the run:
    run.go                        phases, ordering, exits
    prompts.go                    review + fix prompt templates (SPEC Appendix A)
    review.go                     review-file path + freshness + STATUS parsing
    agent.go                      settle / prompt-with-recovery / session reset
    panel.go                      opening and sizing the panel pane
    lock.go                       run lock + panel record
    log.go                        run log, tail, phase, outcome, history archive
internal/ui/                    terminal plumbing and both panes:
    tui.go                        raw mode, key decoding, framing
    style.go                      bold / dim / red / clip
    panel.go  settings.go         the two panes
bin/ensure-binary.sh            download-or-build bin/herdr-review-loop ([[build]] hook)
bin/run.sh                      $HERDR_REVIEW_LOOP_BIN → bin/herdr-review-loop → PATH
bin/run-panel.sh                bin/run-settings.sh
herdr-plugin.toml               manifest: build hook, actions, panes
Makefile                        build · test · tidy · install-plugin
.github/workflows/              ci.yml · release.yml
.golangci.yml  .gitignore  README.md  LICENSE
docs/SPEC.md  docs/TASKS.md
```

Dependency direction: `cmd` → `loop`/`ui` → `config`/`herdr`. No cycles. `loop` reaches
the CLI only through an interface it declares, so tests inject a fake.

### 3.1 Dependencies

Two modules, both `golang.org/x` — the same maintenance tier as the standard library.

| Module | Why | Cost |
| --- | --- | --- |
| `golang.org/x/term` | Raw mode and terminal size for the two panes. | 1 411 LOC, no transitive deps beyond `x/sys` |
| `golang.org/x/sys/unix` | `unix.Flock` for the run lock (§9.1) and `x/term`'s own backing. | Generated syscall tables; only the used symbols link, so the binary does not grow |

Measured: the stripped release build is **2.8 MB**. Terminal handling (~200 lines) and ANSI styling (~10 lines) are deliberately
small and tailored to the two panes. See §9.3 for the alternative that was measured and
rejected.

---

## 4. Command surface

One binary, subcommand dispatch. Manifest actions map to subcommands; the two panes are
subcommands that render a TUI.

| Subcommand | Invoked by | Behaviour |
| --- | --- | --- |
| `herdr-review-loop review` | action `herdr-review-loop.review` | Run the loop. Exit 0 clean, 1 otherwise. |
| `herdr-review-loop review --dry-run` | action `herdr-review-loop.pair` | Resolve and print the pair; prompt nobody, take no lock, write nothing. |
| `herdr-review-loop stop` | action `herdr-review-loop.stop` | Cancel the running loop, from any pane. |
| `herdr-review-loop open-panel` | action `herdr-review-loop.panel` | Focus the workspace's live panel, else open one beside the invoking pane. |
| `herdr-review-loop open-settings` | action `herdr-review-loop.settings` | Open the settings popup. |
| `herdr-review-loop panel` | pane `panel` | The panel TUI process. |
| `herdr-review-loop settings` | pane `settings` | The settings TUI process. Not a TTY → dump settings as text. |
| `herdr-review-loop version` | — | The bare version and nothing else (`0.1.0\n`), injected via `-ldflags -X main.version`. Deliberately unadorned: this output *is* the install-time acceptance test (§7.2), compared whole, so a decorative prefix would have to be parsed back off in shell. |
| `herdr-review-loop help` | — | Usage. Unknown subcommand → usage on stderr, exit 2. |

`open-panel`/`open-settings` exist because a Herdr key binding can invoke an action but
not a pane.

---

## 5. Behaviour

### 5.1 Invocation context

Herdr passes `HERDR_PLUGIN_CONTEXT_JSON` (fields used: `workspace_id`, `workspace_cwd`,
`focused_pane_id`, `focused_pane_cwd`), `HERDR_PLUGIN_STATE_DIR`,
`HERDR_PLUGIN_CONFIG_DIR`, `HERDR_PANE_ID`, `HERDR_BIN_PATH`. State and config dirs fall
back to the working directory so the plugin runs from its own checkout during
development; the herdr binary falls back to `herdr` on `PATH`.

The repository under review is `workspace_cwd`, else `focused_pane_cwd`; neither present
is a fatal error.

### 5.2 Choosing the pair

- **Author** — the agent in `focused_pane_id`. No agent there is a fatal error
  (`run this from your agent's pane`).
- **Reviewer** — candidates are agents in the author's workspace excluding the author's
  own pane, then:
  1. `reviewer_name` set → the candidate whose `name` **or** `pane_id` equals it; no
     match is a fatal error naming the workspace.
  2. `reviewer_kind` set → candidates of that kind.
  3. otherwise → candidates whose kind is non-empty and differs from the author's.
- No candidate → fatal error listing what was found instead.
- More than one → the first from `herdr agent list`, with a note in the log.
- An agent is addressed by `name` when it has one, else `pane_id`; described as
  `kind @ pane_id (name)`.

### 5.3 The loop

```
resolve review-file path (§5.4) · list agents · pick pair · log the run header
dry-run → stop here
mkdir the review file's parent · open the panel if the workspace has none (§5.7)

for i in 1..max_iterations:
    progress token "review i/max" · log "--- iteration i/max: review"
    focus reviewer
    settle reviewer                       (wait out a turn in flight)
    reset reviewer session (§5.5)
    settle reviewer  → ready snapshot
    delete the review file                (after settling, never before)
    askedAt = now
    prompt reviewer with the review prompt, wait for it to settle (§5.6)
    read the verdict (§5.4) · archive it under history/<run-id>/iteration-NN.md
    verdict CLEAN → focus author · notify "clean after i iteration(s)" · exit 0
    log "findings reported (N lines)"
    i == max → break
    progress token "fix i/max" · log "--- iteration i/max: apply"
    focus author · prompt author with the fix prompt, wait for it to settle
exit 1 with "stopped after max iterations with findings still open"
```

Ordering constraints that must survive the rewrite:

- The review file is deleted **after** the reviewer has settled, so a turn still in
  flight cannot recreate it after the delete and have the loop read a stale verdict.
- `askedAt` is captured before the prompt; a verdict file older than it is not this
  round's verdict.
- The author's session is **never** reset: which findings it rejected, and why, is the
  state worth keeping.

Early exits: an agent reporting `blocked` (it is asking the user something) → focus it
and fail with "answer it, then run the loop again"; the reviewer settling without
writing the file within 15s → fail; another run already holding the lock → refuse.

### 5.4 The review file

Every round deletes this path before asking for a verdict, so containment is not a
nicety: a `review_file` that resolves outside the repository turns a typo in a config
file into a delete of something nobody was thinking about.

- **Enforcement is structural, not a prior check.** The run opens the repository once with
  `os.OpenRoot(repo)` and performs *every* review-file operation — `Remove`, `Lstat`,
  `ReadFile`, and the `MkdirAll` of its parent — through that `*os.Root`, addressed by the
  repo-relative `review_file`. `os.Root` refuses `..` and refuses to traverse a symlink
  that leaves the root, on each call, at the moment of the call.
- This is why the check is not "resolve the real path once and trust it afterwards". That
  ordering is a time-of-check/time-of-use gap: the path is validated at startup, but the
  deletes happen minutes later and once per round, and an agent working in the repository
  can replace a validated directory with a symlink in between. Re-resolving before each
  delete would only narrow the window; the rooted handle closes it, and costs less code.
- A resolve-and-compare check still runs at startup — `../notes.md`, an absolute path, or
  a path that is already a symlink out of the tree — but only to fail early with a
  readable message about the setting, rather than mid-run with an `os.Root` error. An
  existing path that is not a regular file is rejected there too.
- A verdict is accepted only when the file's mtime is ≥ `askedAt`; the loop polls every
  500 ms for up to 15 s.
- The verdict is the **first non-empty line**, matched case-insensitively against
  `^STATUS:\s*(CLEAN|FINDINGS)$`. Anything else — including `STATUS: CLEAN, apart
  from…` — is logged and treated as findings. Being wrong in the direction of another
  round is the safe direction.

### 5.5 Resetting the reviewer's session

Built-in commands by agent kind: `claude`→`/clear`, `gemini`→`/clear`, `codex`→`/new`,
`opencode`→`/new`. `reset_command` overrides, or supplies one for an unknown kind; with
neither, the round runs on the existing context and says so in the log.

The command is **typed into the input line**, not sent as a prompt: `agent prompt`
delivers text the way a paste does, and a pasted `/new` is a message the agent drops,
silently leaving every earlier round in context.

```
pane send-text <pane> <command>
sleep 400ms
last 6 non-empty visible lines of `pane read <pane> --source visible --format text`
    contain the command?  → yes: pane send-keys <pane> enter, sleep 1500ms, done
                            no:  pane send-keys <pane> esc, sleep 400ms, retry (3 total)
3 failed attempts → log "kept being dropped", continue with the existing context
```

Only the bottom of the screen is inspected: a review that quotes the command is no
evidence the command was typed.

### 5.6 Prompting an agent

`agent prompt <target> <text> --wait --timeout <ms>` settles on idle/done/blocked.

- Before prompting, the agent must be settled: `agent get`; if `working`, `agent wait
  --timeout`; if `blocked`, focus and fail.
- Error code `agent_prompt_stalled` (herdr saw no lifecycle change in its 5 s window)
  means either the prompt never took or the agent moved faster than detection. Tell them
  apart by `state_change_seq`: poll `agent get` for 5 s for a value above the pre-prompt
  snapshot. Still nothing → the text is sitting unsent in the composer (a multi-line
  prompt arrives as one bracketed paste and the submitting newline is swallowed): press
  enter via `agent send-keys <target> enter` up to 3 times, each followed by a 4 s
  advance check. Any advance → `agent wait --timeout`. None → fail with "the prompt did
  not start a turn".
- Resulting status `blocked` → focus the agent and fail.

Never poll with an unconditional `agent wait`: an idle agent returns immediately and the
loop would read a review that was never written.

**Deadlines.** `review_timeout` bounds the review phase and `fix_timeout` the apply phase
(§6): **one deadline per phase, shared by every call in it**, not one budget handed out
again to each call.

The distinction is the whole point. A review round is not a single CLI call — it is a
`Settle`, then possibly an `agent wait` for a turn in flight, then `agent prompt --wait`,
and on the stalled path another `agent wait` after the enter presses. Give each of those
the full `review_timeout` and a 30-minute setting authorises well over an hour, silently,
with no single call having misbehaved. That is how a phase budget becomes decoration
while appearing to be enforced.

So the phase opens one context — `ctx, cancel := context.WithTimeout(parent, budget)` —
and every blocking call inside it:

- runs under that `ctx` via `exec.CommandContext`, so expiry kills the herdr child process
  rather than merely returning from a wait; and
- passes the **remaining** time (`time.Until(deadline)`) as its own `--timeout`, so herdr
  gets a truthful bound and the last call in a phase gets whatever the earlier ones left.

A remaining budget at or below zero fails the phase immediately instead of issuing a call
with a nonsensical timeout. The fixed waits that are not agent turns — the 15 s for the
review file (§5.4), the 400 ms/1500 ms reset timings (§5.5), the 5 s and 4 s advance
checks above — are constants, and stay outside the budget but inside the context: the
phase deadline still cuts them short.

### 5.7 The panel

A narrow split beside the author, opened by the run when the workspace has no live panel
recorded, or by `herdr-review-loop open-panel`:

```
plugin pane open --plugin herdr-review-loop --entrypoint panel --placement split
    --direction right --target-pane <author> --no-focus --env HERDR_REVIEW_LOOP_AUTHOR=<author>
```

`--no-focus` matters: the loop moves focus between agents and the panel must not take it.
`HERDR_REVIEW_LOOP_AUTHOR` names the author explicitly because the focused pane at pane-start time
is whoever the user is looking at.

**Sizing.** After opening, read `pane layout --pane <id>`, compute the target width —
30 % of the workspace, never below 32 columns (narrower and every log line is an
ellipsis), never above 44, and never more than half the workspace (below ~64 columns no
width suits both and the agent wins) — and nudge with `pane resize --pane <id>
--direction right|left --amount <fraction>` when it is off by more than one column.
Failures are logged and stepped over: a run without its panel is still a run.

**Content**, redrawn every second from files only (nothing is kept in sync with the loop):

```
 herdr-review-loop  ● running            ← lock held by a live process, else "idle"
 author   claude w5:p1         ← resolved every 5s; failure shows "no pair" + reason
 review by codex w5:p2
 2/10 reviewing                ← last "--- iteration i/max: (review|apply)" in the log,
                                 or, when idle, the last outcome line
 ───────────────
 <log tail>                    ← last 8 KiB of the log, timestamps shown at ≥44 columns
 <last key result>
 r review · x stop · s settings · q close      ← wrapped, never clipped
```

Keys: `r` start a run, `x` stop, `s` settings, `q`/`ctrl-c` close. `r` **detaches** the
loop (own session, stdio discarded, context overridden with `focused_pane_id =
HERDR_REVIEW_LOOP_AUTHOR`) — closing the panel must not cancel half an hour of review. `x` and `s`
re-invoke this binary's own `stop` / `open-settings` and show its last line, so
cancelling exists once.

**One panel per workspace.** A panel claims `panel.<workspace>.json` in the state dir by
exclusive create; the loser focuses the winner (`plugin pane focus <pane>`) and exits.
Panels usually die with their pane, which kills them outright and leaves the record
behind, so a dead one is cleared by the next reader rather than by itself.

The record carries the panel's pid **and that pid's start time**, and a record counts as
live only when both still match — the same identity rule as the run lock (§5.9), for the
same reason and with a worse failure mode. A pid alone is not an identity: once the number
is recycled, a record naming a stranger reads as "a panel is already open", and every new
panel closes itself in favour of a pane that is not there, leaving the workspace unable to
open a panel at all. A short-lived advisory guard serializes the read/clear/publish claim
transition, so a stale-record cleanup cannot delete a newly claimed live panel. The guard
is released immediately after claiming; panel liveness still comes only from the record.

### 5.8 The settings pane

A popup (72 % × 60 %) over the workspace, driven entirely by the field registry in
`internal/config` — a field added there appears here with no change to the TUI.

Keys: `↑↓`/`kj` move, `enter` edit (again to accept, `esc` to cancel), `d` restore the
field's default, `s` save, `x` cancel a running loop, `q`/`esc` quit — asking once when
there are unsaved edits. Values still at their default are dimmed. A rejected value shows
the reason at the moment it is typed, not at the start of the next run. The header shows
the config path when idle, or `review loop running (pid N, since HH:MM)`.

Not a TTY (`herdr-review-loop settings` from a shell) → print the config path and every
`label: value` line, exit 0.

### 5.9 Cancelling

`herdr-review-loop stop`, from any pane:

1. No lock held → "no review loop is running", exit 0.
2. Read the holder record: pid, workspace, started, and the pid's **start time** as the
   system reports it (`ps -p <pid> -o lstart=`), recorded when the lock was taken.
   A record that is absent or malformed is **not** a licence to guess — see below.
3. Immediately before **each** signal, re-read the start time for that pid and require it
   to match the record. A pid whose start time no longer matches is not our loop — it
   exited and its number was handed to something else — and is left alone; a pid that no
   longer exists is already gone. Then `SIGTERM`; the loop cancels its in-flight herdr
   call, clears its progress token, releases the lock and exits. Wait up to 3 s, re-check
   identity, `SIGKILL`, wait 1 s.
4. Clear the progress token of the **loop's** workspace (not necessarily the caller's).
5. Report through the log and a notification; exit 1 only when the process survived.

**Publishing the record.** Taking the lock and describing who took it are two operations,
and `stop` can arrive between them. The loop therefore writes the record to a temporary
file and `rename`s it into place — a reader sees the whole record or no record, never half
of one — and does so immediately after `flock` succeeds, before any other work.

**Reading it in that window.** `stop` seeing a held lock with no readable record has
observed a loop mid-startup, or a record damaged by something outside this plugin. It
re-reads a few times over ~1 s, and if there is still nothing it can validate it stops:
`a review loop is starting or its record is unreadable — try again`, exit 1, **no signal
sent, and nothing removed**. Signalling requires a pid that a matching start time proves
is ours, and no amount of inconvenience justifies inventing one; the cost of being wrong
here is a `SIGKILL` delivered to a stranger. Retrying a cancel a second later is the
cheapest possible recovery.

**`stop` never releases the lock — only the holder does.** The lock is held by an open
file descriptor, so it ends when the holder exits or is killed, and `stop` has no step
that deletes or truncates anything. This is worth stating because the Node version *did*
delete an unreadable lock file, and porting that reflex here would be a real regression:
an unreadable record does not imply a dead holder, and clearing the lock while a loop is
still running removes the mutual exclusion that stops two loops from prompting the same
two agents at once. Fail closed; the lock outlives a confused `stop` and dies with its
owner.

**Why the identity re-check exists at all.** Not because the record could be stale — it
cannot, since it is only readable while the lock is held — but because of the window
*after* that observation: the holder may exit between "the lock is held" and the kill, and
again between the two signals. `flock` removes stale *locks*, not stale *pids* (§9.1).

### 5.10 Reporting

- **Log** — `<state-dir>/herdr-review-loop.log`, appended, `[RFC3339] message` per line,
  also written to stdout (Herdr captures it under
  `herdr plugin log list --plugin herdr-review-loop`).
- **History** — every verdict archived to
  `<state-dir>/history/<run-id>/iteration-NN.md`, run id = the RFC3339 start time with
  `:` and `.` replaced by `-`.
- **Progress** — `workspace report-metadata <ws> --source plugin:herdr-review-loop
  --token review=<phase>`, cleared with `--clear-token review` when the run ends however
  it ends. The **source** is the full plugin id, because that is an ownership key; the
  **token name** is the short `review`, because it is displayed — a sidebar row reading
  `$herdr-review-loop` would spend its width on the plugin's name instead of the phase.
  Shows as `$review` on the workspace's sidebar row.
- **Notifications** — `notification show <title> --body <body> [--sound done|request]`
  on clean, budget spent, abort and cancel. A no-op unless the user enabled
  `ui.toast.delivery`.

### 5.11 Prompts

The prompts are behaviour, not prose: the STATUS contract in §5.4 only works because the
reviewer is told to produce it, and the cross-round framing is what stops a reviewer with
no memory from re-litigating findings the author already rejected. Their exact text is
therefore part of this specification — see **Appendix A**, which carries it in full so
this repository is self-contained and nothing has to be fetched from the Node repo to
know what the loop says.

Prompts live in `internal/loop/prompts.go` as templates over `{scope}` (from config),
`{review_path}` (absolute), `{iteration}` and `{max}`, with a golden test pinning the
rendered output for round 1 and a later round, so an accidental edit shows up in a diff.

---

## 6. Configuration

`config.json` in `herdr plugin config-dir herdr-review-loop`; only values differing from the
defaults are stored, written to a temp file and renamed so a reader never sees half a
config.

| Key | Kind | Default | Meaning |
| --- | --- | --- | --- |
| `reviewer_kind` | optional string | `null` | Kind that reviews; empty = any kind other than the author's |
| `reviewer_name` | optional string | `null` | Pin one live agent by name or pane id; wins over kind |
| `max_iterations` | int 1..1000 | `10` | Review/apply rounds before giving up |
| `review_file` | repo-relative path | `review.md` | The reviewer's only output |
| `review_timeout` | duration | `"30m"` | Budget for one review round |
| `fix_timeout` | duration | `"30m"` | Budget for the author to apply a review |
| `reset_command` | optional string | `null` | Overrides the built-in reset for the reviewer's kind |
| `scope` | non-empty string | "the uncommitted changes in the working tree, plus any commits on the current branch that are not on the default branch" | Pasted into the reviewer's prompt verbatim |

Validation rules, shared between the file loader and the settings pane so a value is
judged the same whichever way it arrives:

- A value the loop cannot use is **never guessed at**: it falls back to its default and
  the reason is logged. An unknown key is reported and ignored. A `config.json` that is
  not a JSON object yields all defaults plus one warning — it must not take down
  `herdr-review-loop stop`.
- `review_file` must be non-empty, relative, not escape the repository after
  normalisation, and not name a directory. (Real-path containment is the loop's half of
  the check — §5.4.)
- Durations are `time.ParseDuration` strings, must be > 0, rendered back in shortest form.
- `max_iterations` must be a whole number in range; a budget nobody would sit through is
  a typo.

---

## 7. Packaging and install

### 7.1 Manifest (`herdr-plugin.toml`)

```toml
id = "herdr-review-loop"
name = "herdr-review-loop"
version = "0.1.0"
min_herdr_version = "0.7.5"
description = "Review loop between the agent you are in and a reviewer agent in the same workspace."
platforms = ["linux", "macos"]

[[build]]                                    # runs on every install, incl. update
command = ["bash", "bin/ensure-binary.sh", "--in-tree"]

[[actions]]  review · pair · stop · panel · settings
[[panes]]    panel (split) · settings (popup, 72% × 60%)
```

Actions run `bash bin/run.sh <subcommand>`; panes run `bin/run-panel.sh` /
`bin/run-settings.sh`. `stop` and `settings` are also available in the `global` context;
the rest are `pane` + `workspace`.

The `[[build]]` hook is the whole reason install works: `herdr plugin install` replaces
the managed directory with a fresh checkout that never carries the gitignored binary, and
there is no separate update command.

### 7.2 `bin/ensure-binary.sh` — download first, build second

The reference builds first and downloads as a fallback, because its maintainers develop
against installs. For an end-user plugin the priority is inverted: **the released binary
matching this checkout's manifest version is the answer, and compiling is the fallback.**
No Go on the machine is the normal case, not the degraded one.

`VERSION` is read from `herdr-plugin.toml`. Every path below ends in the same acceptance
test — **the entire trimmed stdout of `bin/herdr-review-loop version` equals `$VERSION`**
— so "the binary is present" and "the binary matches this checkout" are never confused.
The comparison is whole-output on purpose, which is why §4 specifies that command to
print the bare version: a `name x.y.z` line would need parsing here, and a parser is a
place for the check to pass when it should not.

`--in-tree` (the manifest hook), in order:

1. `bin/herdr-review-loop` present **and** reporting `$VERSION` → done. A binary reporting anything
   else is stale (a checkout updated over an older build) and is replaced by the steps
   below rather than accepted.
2. Download `herdr-review-loop-<os>-<arch>` **and `SHA256SUMS`** from the release tagged
   `v$VERSION` (`gh release download` when `gh` is installed and authenticated, else
   `curl -fsSL https://github.com/.../releases/download/v$VERSION/<name>`), verify the
   asset against **its own line** in `SHA256SUMS`, `chmod +x`, then apply the acceptance
   test. Any failure — download, missing checksum file, no line for this asset, checksum
   mismatch, wrong version — discards the file and continues to step 3.

   The checksum file covers all four platform assets while only one was downloaded, so the
   line for this asset is extracted before checking — handing the whole file to `-c` fails
   on the three absent assets and would reject every valid release. The verifier itself is
   whichever of the two is present, because neither is available everywhere: `sha256sum`
   is coreutils and standard on Linux but absent from a stock macOS, while `shasum` is a
   Perl script that macOS ships and many minimal Linux images do not:

   ```bash
   if command -v sha256sum >/dev/null 2>&1; then check() { sha256sum -c -; }
   elif command -v shasum   >/dev/null 2>&1; then check() { shasum -a 256 -c -; }
   else  # no verifier: fall through to step 3 rather than install unverified
   fi
   grep " $ASSET\$" SHA256SUMS | check
   ```

   Both accept the same `-c -` line format, so only the invocation differs. A host with
   neither tool does not get an unverified install; it gets step 3.
3. `go build -o bin/herdr-review-loop ./cmd/herdr-review-loop`, with `-ldflags "-X main.version=$VERSION"`, when
   a Go toolchain is present, then the acceptance test. This is the path for an unreleased
   commit — `herdr plugin install --ref <branch>`, or a manifest version with no release
   yet — where step 2 correctly finds nothing.
4. Otherwise fail the install, naming both fixes: install Go (then `make build`), or
   install `gh`/`curl` and retry against a released version.

**Only the exact tag is ever downloaded.** There is no `latest` fallback: a binary from
some other release is a binary whose behaviour need not match this checkout's manifest,
actions and pane entrypoints, and installing one silently is the failure mode step 2's
tag pinning exists to prevent. An unreleased checkout on a machine without Go is a real
dead end, and saying so beats papering over it.

The checksum step is transfer integrity, not authenticity: the asset and `SHA256SUMS`
come from the same release, so it detects a truncated or corrupted download and a
mismatched asset, and does not defend against a compromised release. A missing
`SHA256SUMS` is treated as a failure rather than skipped — our own release workflow always
publishes one, so its absence means this is not a release we should be installing.

Other modes: `--build` always compiles (same `-o` and `-ldflags`) and fails hard
(`make build` uses it); no argument ensures the binary is resolvable the way
`run-herdr-review-loop.sh` resolves it (`$HERDR_REVIEW_LOOP_BIN` → `bin/herdr-review-loop` → `PATH`).

Developers are unaffected: `herdr plugin link .` + `make build` always compiles, and the
version injected there is the manifest's, so a local build satisfies step 1 instead of
being replaced by a download.

### 7.3 Install (README)

```bash
herdr plugin install mikhail-angelov/herdr-review-loop     # --yes in non-interactive shells
herdr server reload-config                                 # after adding keybindings
```

No Go, no Node, no post-install step beyond the keybindings the README prints.

Development install: `make build && herdr plugin link .`.

Migrating a Node-era `config.json`: rename `review_timeout_ms` / `fix_timeout_ms` to
`review_timeout` / `fix_timeout` and write the value as a duration string
(`1800000` → `"30m"`). Unknown keys are reported and ignored, so a stale file degrades to
defaults rather than breaking a run.

### 7.4 CI and release

Two workflows, no more.

- **`ci.yml`** — push to `main` + every PR, `permissions: contents: read`. One matrix job
  on `ubuntu-latest` and `macos-latest`: `go vet ./...`, `go build ./...`,
  `go test ./...`, and `gofmt -l .` must print nothing. The `./...` is required, not
  stylistic: every package lives under `cmd/` or `internal/`, the repository root holds no
  `.go` files, and the bare forms exit 1 with `no Go files in …` — CI would be red from
  the first commit and green only once someone "fixed" it by dropping the step. One
  `golangci-lint` job on Linux via
  `golangci/golangci-lint-action@v9` with `version: latest`. Go pinned to the `1.26`
  minor line, patch-floating (so stdlib patches land without a commit). golangci-lint
  v2.12.2 is itself built with Go 1.26, so `latest` analyses a `go 1.26.0` module
  correctly.
- **`release.yml`** — on `v*` tags: re-run the same four checks (with `./...`), then
  cross-compile `darwin/{arm64,amd64}` and `linux/{amd64,arm64}` into `dist/` with
  `CGO_ENABLED=0 -trimpath -ldflags "-s -w -X main.version=${GITHUB_REF_NAME#v}"`, then
  emit the checksums **from inside `dist/`** so the file lists bare asset names:

  ```bash
  (cd dist && sha256sum herdr-review-loop-* > SHA256SUMS)
  ```

  `sha256sum` rather than `shasum -a 256` because this always runs on the Ubuntu runner,
  where coreutils is guaranteed and Perl is only conventional; the two emit byte-identical
  output, so the installer's verifier choice (§7.2) is unaffected either way.

  Running it as `sha256sum dist/* > dist/SHA256SUMS` instead would record
  `dist/herdr-review-loop-…` in every line, which the installer's per-asset lookup (§7.2)
  cannot match and which `-c` would then look for under a `dist/` that does not exist on
  the user's machine. The `cd` is the contract.

  Then publish. The step is written out because each part of it is load-bearing:

  ```yaml
  permissions:
    contents: write        # the default token is read-only; without this, publish 403s
  ...
      - name: Create release
        env:
          GH_TOKEN: ${{ github.token }}    # gh reads no token from the runner otherwise
        run: |
          gh release create "$GITHUB_REF_NAME" dist/* \
            --title "$GITHUB_REF_NAME" \
            --generate-notes
  ```

  The tag is named explicitly and `dist/*` names the four binaries plus `SHA256SUMS`:
  `gh release create` neither infers the tag nor uploads anything on its own, and a
  release published without assets is one that §7.2 step 2 cannot install from.

Releasing is `git tag vX.Y.Z && git push origin vX.Y.Z` after bumping `version` in the
manifest; `release.yml` re-verifies before publishing, so there is no separate gating
script.

**The one contract to keep:** asset names in `release.yml` (`herdr-review-loop-<os>-<arch>`), the
platform mapping in `ensure-binary.sh`, and `version` in `herdr-plugin.toml` are a single
unit. Change one without the others and install silently downloads the wrong thing — hence
the version smoke test in §7.2 step 2.

---

## 8. Testing

Pure cores are separated from I/O, and the herdr CLI is consumed through an interface the
loop declares, so a fake covers the whole run without a live server.

| Area | Tests |
| --- | --- |
| `herdr` client | JSON envelope: success, `error.code`/`message`, non-JSON stderr, non-zero exit |
| `herdr` agents | Pick precedence (name/kind/cross-review), pane-id fallback, wrong workspace, zero candidates, multiple candidates |
| `config` | Every field × (valid, invalid, empty, out of range); unknown key; non-object file; save/load round-trip stores only non-defaults |
| `loop/review` | STATUS matrix (clean, findings, leading blank line, casing, trailing prose, absent); mtime freshness; real-path containment incl. a symlink out of the repo; a directory at the path |
| `loop/agent` | Prompt accepted; stalled but sequence advanced; stalled then enter press #2 works; nothing works → error; blocked → focus + error; reset first-try / retry / three failures / unknown kind |
| `loop/run` | Against the fake: clean on round 1; findings→fix→clean; budget spent (exit 1); blocked agent; reviewer never writes the file; dry run takes no lock and writes nothing |
| `loop/panel` | Width computation at 40 / 64 / 100 / 200 columns; resize direction and amount |
| `loop/lock` | Second acquire refused while held; free after the holder is SIGKILLed; two panel claims → one winner |
| `loop/log` | Tail beyond the window, phase and outcome extraction, no-match log |
| `ui/tui` | Key decoding: a CSI sequence split across two reads; a bracketed-paste marker not read as five keys; application-cursor arrows (`ESC O A`); a lone `esc` still delivered after the partial-sequence window |
| `ui` | Panel view at 32 / 44 / 80 columns; settings edit→reject→fix→save; quit-confirm |

CI-enforced: `gofmt`, `go vet`, `go test`, `golangci-lint`. `.golangci.yml` is the v2
default set plus the reference's `SA5011`-in-tests exclusion — no bespoke linter tuning
until something actually annoys us.

---

## 9. Deliberate changes from the Node version

**9.1 The lock becomes an OS lock.** Today's `link()`-based lock cannot be taken over
safely when its holder dies (POSIX offers no compare-and-swap on a path), so a crashed
run must be cleared by a manual cancel. `unix.Flock` is released by the kernel when the
holder dies, so a crashed run leaves nothing behind and the next run starts.

What this does **not** retire is the `ps`-based identity check. The sidecar record is only
readable while the lock is held, so it can never name a dead process at the moment it is
read — but `stop` signals *after* that read, and the holder can exit in between, handing
its pid to a stranger. `flock` fixes stale locks; it does nothing about stale pids, so the
start-time check survives from the Node version and runs before every signal (§5.9).

**9.2 Cancellation becomes graceful.** The Node loop blocks in synchronous CLI calls
where its own signal handler would not run until the wait returned, so `stop` kills it and
cleans up on its behalf. In Go the loop runs its herdr calls under a context: SIGTERM
cancels the in-flight call, and the loop clears its own progress token, releases its own
lock and logs its own cancellation. `SIGKILL` stays as the fallback.

**9.3 The TUI is deliberately small, not framework-based.** It hand-rolls raw mode,
escape-sequence reassembly across reads, bracketed-paste detection and framing — a small
enough surface that a framework would cost more than it saves. Bubbletea was measured and rejected:

| | `x/term` + local terminal code | bubbletea |
| --- | --- | --- |
| modules in the tree | 2 (`x/term`, `x/sys`) | 17 |
| third-party LOC | ~1.6k | ~54.7k |
| binary | 2.8 MB | additional measurement required |
| our code | ~200 lines | model boilerplate |

Bubbletea would add a larger terminal abstraction and its dependency graph to a pane that
needs only a full-frame redraw and a handful of keys.

What the panes actually need is one full-frame redraw per tick and four to six keys — no
mouse, no alt-screen, no scrolling viewport, no cursor-addressed text input. The two
subtle parts (an escape sequence split across two reads, and bracketed-paste markers
being read as five keystrokes the first of which means "quit") are handled locally.
Revisit if a pane grows a scrolling viewport or a real text
editor.

**9.4 Durations become strings.** `"30m"` instead of `1800000`, parsed by
`time.ParseDuration` instead of a custom `90s|30m|2h|ms` regex.

**9.5 Node stops being a runtime dependency,** and install stops needing a toolchain at
all (§7.2).

**9.6 The reference's process machinery is not copied.** `herdr-agent-usage` is a
multi-provider, multi-maintainer project; most of its repository furniture is priced for
that and would be pure overhead here. Left out, with the reason:

| Not adopted | Why |
| --- | --- |
| commitlint config + `make install-hooks` + `lint-pr-title.yml` | Three moving parts, one of which silently repoints `core.hooksPath`, to lint the commit messages of a single-author repo. Conventional Commits by habit, not by tooling. |
| `CONTRIBUTING.md`, `AGENTS.md`, PR template | The rules that matter here are §5 (behaviour to preserve) and §3 (four packages, one direction). A README section says that in a paragraph. |
| `dependabot.yml` | Two direct dependencies. `go get -u && make test` when it matters. |
| `govulncheck` job | The binary shells out to a local CLI and reads local files; it parses no untrusted network input. Revisit if that changes. |
| `scripts/release.sh` (CI-gated tagging) | 40 lines of shell to prevent tagging a red commit, when `release.yml` re-verifies before publishing anyway. |
| `setup` action, update-check | Keybinding snippets belong in the README; an update checker is a feature to add when the release cadence justifies one. |
| Per-concern packages (11 → 4) | `internal/loop/lock.go` is a file. It was going to be a package because the reference has one per concern; there is no second consumer and no seam. |

What *is* adopted, because it is load-bearing: the `[[build]]` hook, `ensure-binary.sh`,
the `run-*.sh` resolution scripts, cross-compiled release assets, and CI on both
platforms. That is the install story, and it is the whole reason to look at that repo.

Everything from the Node version not listed in 9.1–9.5 — the prompts, the STATUS
contract, the reset-by-typing dance, the stalled-prompt recovery, the panel sizing, the
delete-after-settle ordering — is preserved deliberately. Each encodes a failure that was
already paid for once.

---

## 10. Definition of done

1. `make build && make test` clean; `gofmt -l .` empty; `go vet` and `golangci-lint` clean
   on Go 1.26. `go list -m all` shows two modules, both `golang.org/x`; the release binary
   is ≈3 MB.
2. `herdr plugin link .`, then from a workspace with a claude pane and a codex pane:
   `herdr-review-loop.pair` names the right pair; `herdr-review-loop.review` runs a full loop to a clean
   verdict; `herdr-review-loop.stop` cancels a running loop from a third pane; the panel opens at a
   sane width, tracks the phases and starts a run with `r`; the settings pane edits,
   saves, rejects a bad value and cancels a run.
3. `herdr plugin install mikhail-angelov/herdr-review-loop` on a machine **without Go**
   installs the release asset and both panes come up. Verified by hiding the toolchain
   (`PATH` without `go`) on a clean checkout.
4. README covers install, keybindings, panel, settings, cancellation, the STATUS contract,
   every config key, and the `config.json` migration.
5. `git tag v0.1.0 && git push origin v0.1.0` publishes four assets plus `SHA256SUMS`.

---

## 11. Resolved decisions

- **Install without a toolchain** — yes, and it is now the primary path (§7.2).
- **Go 1.26 + golangci-lint latest** — supported: golangci-lint v2.12.2 is built with Go
  1.26 and its go1.26 support issue is closed. CI floats the 1.26 patch line.
- **bubbletea** — measured and dropped (§9.3): 17 modules and +32 % binary for a redraw
  loop and six keys, when the proven 124-line `tui.js` ports directly onto `x/term`.
  `gofrs/flock` went with it — `x/sys/unix` is already in the tree for `x/term`, and
  `unix.Flock` is fifteen lines.
- **`herdr-review-loop.setup` action** — dropped; the README prints the snippets.
- **Update checking** — dropped until release cadence justifies it.
- **Reviewer selection with >2 agents** — unchanged: first candidate wins, `reviewer_name`
  to pin. A picker would be a behaviour change, not a rewrite concern.

---

## Appendix A — Prompt templates (normative)

Copied from the Node implementation's `index.js` (`reviewPrompt`, `fixPrompt`) at
`hreview@517458be48ea0e72c6c035a542517cae16547d60`. **This appendix is the normative
copy**; the upstream repository is being deleted and is not needed to maintain this one.

### A.1 Review prompt

The opening paragraph on **round 1**:

```
You are the reviewer in an automated review loop. This is round {iteration} of {max}.
```

The opening paragraph on **every later round** — the reviewer's session was just wiped,
so it must be told what it cannot remember, and told not to invent it:

```
You are the reviewer in an automated review loop. This is round {iteration} of {max}, and your session was cleared beforehand, so you have no memory of the earlier rounds and should not try to reconstruct them. The code has already been through {iteration-1} round(s) of review; some findings were applied and others were deliberately rejected by the author. Judge the code as it stands now, on its own merits.
```

The body, identical on every round, appended after a blank line:

```

Review {scope}.

Write the review to {review_path}, overwriting whatever is there.
The first line of that file must be exactly one of:

STATUS: CLEAN
STATUS: FINDINGS

Use STATUS: CLEAN only when nothing is left that is worth changing. Otherwise use
STATUS: FINDINGS and list every finding underneath, one per bullet, as:

- [high|medium|low] path/to/file.ext:LINE — what is wrong — what to do about it

Do not edit any code yourself; the review file is your only output. Reply with just
the path when you are done.
```

### A.2 Fix prompt

```
A review of your changes is in {review_path}.

Read it and apply every finding you agree with. Deliberately skip the ones you
consider wrong or out of scope. Do not edit {review_path}.

When you are done, summarize in a few lines what you applied and what you rejected
and why — the reviewer will re-review from the code, not from your summary.
```

### A.3 Notes for the implementation

- `{scope}` is substituted verbatim from config (§6) and is a noun phrase: the sentence
  reads `Review the uncommitted changes in the working tree, plus …`.
- `{review_path}` is the absolute path resolved in §5.4, not the configured relative one —
  the reviewer is told where to write, not asked to build a path.
- Line breaks are significant. The em dashes in the finding format are U+2014 and are what
  a reviewer copies into its output; the STATUS lines must survive as their own lines,
  because §5.4 matches the first non-empty line exactly.
- The golden test renders both prompts for `{iteration}` = 1 and 3 with a fixed scope and
  path, and compares against checked-in fixtures.
