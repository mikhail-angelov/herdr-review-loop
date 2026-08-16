# herdr-review-loop v2 — Specification: findings as data

Successor to [SPEC.md](SPEC.md), which describes the plugin as built. This document is
normative for everything it covers and silently keeps SPEC.md elsewhere: the pane model,
the run lock, session resets, checkpoints, packaging and the install path are unchanged.

Prior art consulted: [umputun/revmux](https://github.com/umputun/revmux). What is borrowed
is named where it is borrowed; what is deliberately not borrowed is in §2.2.

---

## 1. Why

Three problems, all structural.

**The review contract is prose.** The reviewer writes `STATUS: CLEAN|FINDINGS` and a list
of bullets, and `ParseVerdict` treats anything it cannot read as findings. That bias is
right and stays, but it means a garbled model turn costs a whole round — review, fix,
review again. It also means a finding is not a thing the loop can hold: it cannot be
filtered, matched against the same finding in the next round, or reported on at the end. A
finished run leaves nothing that explains it, so no change to the loop can be justified by
anything but taste.

**The loop's own files sit in the diff under review.** `review.md` and
`review-summary.md` live in the working tree, which is exactly the scope being reviewed.
Both prompts carry a line telling the reviewer not to report findings about them, round
checkpoints record their churn, and `finish` exists in part to delete them. The run's most
visible artifacts are scaffolding the user has to be told to ignore.

**The loop pretends to know how to review.** `ReviewPrompt` carries a hand-written review
taxonomy — correctness, security, missing tests — competing with the review capability the
agents already ship and maintain (§6.1). Every hour spent tuning that prompt is an hour
spent rebuilding, worse, something that already exists.

v2 makes findings data, takes the data out of the working tree, delegates *what to look
for* to the reviewer, and archives what a run produced (§5) so a finished run can still be
explained afterwards.

Each of those four is load-bearing for the next; everything that was not, §2.2 says why it
is not here.

---

## 2. Scope

### 2.1 In

1. Run artifacts move out of the reviewed diff (§3).
2. A JSON review contract with loop-assigned ids and fingerprints (§4).
3. A per-run archive of what the run was sent, produced and changed (§5).
4. Delegation of the review itself to each agent kind's native review command, with the
   round policy as the only thing the loop still owns (§6).
5. Stall detection, retries, distinguishable exit codes, stuck detection (§7).
6. Verdict filtering instead of a verification stage (§8).
7. A `text:` scope, for reviewing a document rather than a diff (§9).

### 2.2 Out, deliberately

- **A lens library of our own.** Cut in favor of §6. The reviewer's own taxonomy, severity
  bar and verification pass are better maintained by the people who calibrate them.
- **Multi-agent fan-out.** Already available to the user as `/code-review ultra` inside
  the reviewer pane. Building a second one would be money and minutes for something one
  line of profile configuration can request.
- **revmux as a review backend.** It would double the install requirements, duplicate what
  the agents already do, and it is not a direction: if reaching a wider audience matters,
  the answer is extracting the loop into its own binary with the herdr plugin as one
  frontend.
- **Any git history operation.** No commits, no amends, no rebase. The loop edits the
  working tree and nothing else.
- **Calling models over an API.** Driving live herdr panes is what buys subscription auth
  and support for every agent kind, including gemini and opencode.
- **Backward compatibility.** The flat `config.json` keys are replaced, not mapped; the
  Node migration section leaves the README. Per repository policy, compatibility is not
  preserved.
- **Mining the findings for rules.** The permanent `findings.jsonl` corpus, the `findings`
  export, the `stats` command and the cross-run `pattern` key were all specified and are all
  cut together, because they served one idea that is not this tool's job: accumulate every
  finding across every run, notice which recur, and turn them into `CLAUDE.md` rules. That is
  a separate product with its own data model, and building it inside a loop whose job is to
  converge one diff bought a permanent store, an export subcommand, a reporting subcommand
  and a hash that could not group anything — for a payoff that needs hundreds of runs to
  arrive. **The loop needs its artifacts kept somewhere; it does not need a dataset.** §5 is
  therefore one per-run archive, rotated. Anyone who wants the cross-run view later has every
  byte of it in the archives and can write that tool against them.
- **A cross-run `pattern` key.** Gone with the above, and independently wrong:
  `sha256(category, title)` only groups findings a model phrased identically twice, which is
  not how models write, so the key would be nearly unique and would group nothing.
  `fingerprint` stays — it is matched within one run, where the reviewer is one agent in one
  session and repetition is real (§4.2).
- **Reading the project layer from `HEAD`.** Cut. It defended the round policy against a
  branch that edits `.review-loop/` — but a branch that edits `CLAUDE.md` or `AGENTS.md`
  redirects the same review just as effectively, and nothing here can freeze those, because
  the reviewer reads them itself. A defense with a hole that size is not a defense; keeping
  it would have bought a `git show` path, per-file fallbacks and a provenance record for the
  appearance of one. The loop reads the project layer from the working copy like every other
  file, and the README says plainly that the review policy is a convenience, not a boundary,
  and that a diff which touches it deserves a human's eye.
- **`staged` and `branch` scopes.** Cut to `worktree` and `text:` (§9). Since the author may
  only edit the working tree, every scope has to end there anyway, which left `staged` and
  `branch` differing from `worktree` mostly in the prose sent to the reviewer — for a page of
  specification, a native-flag special case on round 1, and a merge-base to record. They
  join `commits:` and `paths:` under the same rule: implemented when someone asks.
- **YAML, front matter, and profile bodies.** Profiles are JSON with the same schema as
  `config.json` (§6.3). A second format meant a second parser and a new dependency for the
  sake of a markdown body nothing but the round `instructions` field needed.

---

## 3. Run artifacts and the working tree

### 3.1 Layout

```
<repo>/.review-loop/              committed by the project
├── config.json                   per-key override of user config
└── profiles/<name>.json           round policy (§6.3)

<repo>/.review-loop/run/          the active run, excluded from git
├── scope.md
├── round-01/
│   ├── review.json               written by the reviewer
│   └── decisions.json            written by the author
└── round-02/
```

`.review-loop/run/` is created `0o700`, files `0o600`. The loop hands agents **absolute**
paths, and every path it hands out is under the repository, so an agent confined to its
workspace sandbox can write there. This is why the run directory is not in the plugin
state directory: codex and claude may refuse writes outside the workspace.

### 3.2 Excluding the run directory

At run start the loop appends `/.review-loop/run/` to `.git/info/exclude` if that line is
not already present. `info/exclude` is per-clone and untracked: no file the user owns or
commits is modified, and the project is free to commit `.review-loop/` itself.

Consequences, all of them wanted:

- The run's files never appear in `git diff`, so the reviewer never sees them and the
  "do not report findings about …" clause leaves both prompts.
- `Checkpoints` stages with `git add -A`, which honors the exclude file, so round
  snapshots contain the author's changes and nothing else.
- `changes.patch` (§5.1) is exactly what the round changed.

All three rest on git actually ignoring the path, and `info/exclude` does not ignore what
git already tracks. A repository that has committed `.review-loop/run/` — or anything under
it — by accident would keep staging the run's files with `git add -A`, put them in the
reviewed diff and in every checkpoint, and the containment claim above would be false while
the exclude line sat there looking effective. Preflight (§3.3) therefore asks git for
tracked paths under `.review-loop/run/` and **refuses to start** when there are any: exit
code 2, naming the paths and the `git rm -r --cached .review-loop/run` that fixes it.
Untracking is the user's move, not the loop's — the loop touches no index and no history
(§2.2), and silently ignoring the paths for one run would leave the next run broken again.

Outside a git work tree there is nothing to exclude and nothing to hide from; the loop
skips this step and the tracked-path check with it.

### 3.3 Preflight

Before taking the run lock the loop opens the repository root as an `os.Root`. Every later
access to `.review-loop`, `.review-loop/run`, a `round-NN` directory and the files inside
it goes through that handle and no other — create, open, read, write. Each component must
be absent or a real directory; a symlink, a regular file or anything else at any of those
names fails with a message naming the component, whether it was there at preflight or
appeared later, and no path under `.review-loop/run/` may be tracked by git (§3.2). A
`run/` left behind by a crashed run is removed and recreated once the
lock is held, so a stale directory never becomes one the loop writes into blind, and
`round-NN` must not exist when that round's **review phase** starts — the review phase is
what creates it, and that is what makes §3.4 freshness hold. The author phase of the same
round is the one exception, and a deliberate one: it reuses the directory the review phase
created, because that is where `review.json` it must read lives and where `decisions.json`
it must write belongs. What must be absent when the author phase starts is
`decisions.json`, not the directory.

The check covers the whole path, not just its first component, because the containment
claim in §3.1 is about the files the loop hands to agents as absolute paths: a symlinked
`run` or `round-NN` would redirect both the loop's writes and the agents' writes outside
the repository while every individual path still looked repository-relative. This replaces
the `review_file` setting and the `os.Root` guard that used to wrap it — the path is now
owned by the plugin rather than configured by the user, but rooted access is kept, since
ownership of a name is not ownership of what it points at.

### 3.4 Freshness

The review phase creates `round-NN` before prompting, and §3.3 guarantees the directory did
not exist beforehand, so the presence of `round-NN/review.json` *is* proof that this round's
reviewer wrote it. The author phase creates nothing: it opens that same directory, reads
`review.json` from it, and requires `decisions.json` to be absent when it starts — which
gives the author's output the same proof for the same reason. The mtime-versus-`askedAt`
comparison and its one-second truncation are removed.

A file that exists but does not parse counts as not yet written while the agent's turn is
running — that is a partial write. Once the turn settles, an unparsable file enters the
degradation ladder (§4.5).

### 3.5 Markdown is a view, never a file

No markdown is written to the working tree. `review.md` and `review-summary.md` are gone.

```
review-loop show [--run <id>] [--round <n>] [--format md|json]
```

renders the run, defaulting to the active or most recent run, the last round, and `md`.
The history pane renders through the same function and pipes it to `$PAGER`, so there is
one renderer and no artifact to drift out of sync.

### 3.6 What `finish` becomes

`finish` archives the run (§5), removes
`.review-loop/run/`, and closes the panel. It no longer deletes anything the user might
have edited, and the README states plainly that the loop's only output in the working tree
is the code.

---

## 4. The review contract

### 4.1 `review.json`

```json
{
  "status": "findings",
  "findings": [
    {
      "file": "internal/loop/run.go",
      "line": 88,
      "end_line": 0,
      "category": "correctness",
      "severity": "high",
      "verdict": "confirmed",
      "title": "context is not canceled on the timeout path",
      "body": "one to three sentences on why this is a problem",
      "fix": "what to do about it",
      "regression": false
    }
  ],
  "open_questions": [
    {
      "question": "is the 5m stall budget meant to cover a reformat turn too?",
      "file": "internal/loop/watchdog.go",
      "line": 0
    }
  ],
  "pre_existing": []
}
```

The field names follow what the agents' own review commands already emit, so the adapter
(§6.4) is a rename and not a translation.

| Field | Rule |
|---|---|
| `status` | `clean` or `findings`. Advisory — §4.6 resolves it against the array |
| `file` | repository-relative. A path outside the repository drops the finding and logs `degraded: path` |
| `line` | `0` means the file as a whole, which is legal |
| `end_line` | `0` when the finding is one line |
| `category` | the reviewer's own category slug, free-form. **The loop never branches on it**; it is carried through to the report (§5.4) and grouped on there |
| `severity` | `high\|medium\|low`, optional. Absent means `medium`; array order is the reviewer's own ranking and is preserved |
| `verdict` | `confirmed\|plausible`, optional, defaults to `confirmed`. §8 filters on it |
| `regression` | this broke in an earlier round's fixes. Only meaningful from round 2 |
| `pre_existing` | already true before the diff. Not sent to the author; kept for the report (§5.4) |
| `open_questions` | entries are `{"question": <one sentence>, "file": <repository-relative, optional>, "line": <int, optional, 0 for the file as a whole>}`. Something the reviewer could not settle from the diff and that needs a human. A non-empty array ends the run under §4.6; an entry without a `question` string is dropped and logged `degraded: open-question` |

### 4.2 Ids and fingerprints belong to the loop

Models do not hold stable identifiers across turns, so they are not asked to.

| Key | Value | Purpose |
|---|---|---|
| `id` | `r{round:02d}-{n}` → `r01-3`, unique in a run | what the author decides on (§4.7) |
| `fingerprint` | `sha256(file, category, normalize(title))[:12]` | the same finding across rounds of one run: suppressing settled points, detecting a stuck loop (§7.4) |

`normalize` lowercases, collapses whitespace and strips punctuation. Exact-match hashing is
enough here and only here: within one run the reviewer is one agent in one session re-reading
one diff, so it does repeat a title nearly verbatim. Across runs and sessions it does not,
which is why there is no cross-run key — recognizing that two differently-worded findings are
the same *kind* of problem is a reader's job, and not one this loop takes on (§2.2).

### 4.3 How the review is requested

The loop does not describe what to look for. Per round it sends the reviewer:

1. the agent kind's **review command** at this round's level (§6.2), carrying the scope
   and the round's extra instructions as the command's own custom-instruction argument;
2. the decision journal of prior rounds (§4.7);
3. a **capture step**: when the review is finished, write the findings as JSON matching
   §4.1 to the absolute path of `round-NN/review.json`, and change no code.

The capture step exists because the native commands render findings into the host UI
rather than a file. The findings are already in the agent's context at that point, so
serializing them is cheap and lossless.

### 4.4 What the reviewer is never told

No taxonomy, no severity definitions, no "look for races and unhandled errors". The
project's own review standards live in its agent configuration — `CLAUDE.md`, `AGENTS.md`,
project skills — where every review benefits from them, not only this loop. Keeping those
files current is the project's job and not something this loop automates (§2.2).

### 4.5 Degradation ladder

Applied in order to the reviewer's output once its turn has settled:

1. Parse the **last** top-level JSON object in the file, stripping ``` fences. Last, not
   first, because models like to show an example and then give the real answer.
2. Fall back to the v1 markdown form (`- [high] path:LINE — what — what to do`). Record
   `parse_fallback: markdown`.
3. Send **one** reformat turn to the same agent: *here is your review, write it as JSON
   matching this schema to this path, add nothing.* The context is already warm.
4. Hand the failure to §7.2: reset the session and repeat the review phase, once per
   remaining retry in the configured budget. The raw output goes to the archive unless
   `archive.raw_output` is off (§5.1) and the round is flagged `degraded: parse`.
5. A parse failure that outlives the retry budget stops the run with code 5 (§7.3). The
   panel names the failure, and the last raw output is archived on the same terms as
   step 4.

Step 3 is what stops a garbled turn from costing a full round; steps 4 and 5 are the same
ladder §7.2 applies to stalls and crashes, and there is only one of them. The loop never
enters an author phase without structured findings: an author given prose has no ids to
decide on, so §4.7's stop condition would fire on the next round anyway, one wasted phase
later.

### 4.6 The loop resolves contradictions, not the model

- `status: clean` with a non-empty `findings` array → the array wins, the round is dirty.
- `status: findings` with an empty array → not a verdict. In practice it is a truncated
  turn rather than clean code, so it must never be the thing that produces exit code 0. It
  enters the ladder at §4.5 step 3: one reformat turn, then the §7.2 retry budget, then
  stop with code 5. If a retry answers `status: clean` with an empty array, that is clean; if the
  contradiction repeats, the run ends unresolved and flagged `degraded: empty-findings`,
  never clean.
- a non-empty `open_questions` array, whatever `status` says and whatever the findings
  array holds → the run stops with **code 3** (§7.3), before any author phase. A reviewer
  that has to ask cannot also be read as clean, and an author cannot answer a question
  addressed to a human. The questions are named in the panel and in `report.json`,
  recorded as a `blocked` event (§5.3), and any findings raised in the same round are
  archived and recorded as `unreviewed` (§5.4) — the run still leaves everything it
  learned. Resuming is the user's move after answering, exactly as for any other code 3.

The rule behind all three: a clean verdict is only ever read from output the loop could
parse, that agrees with itself, and that asks nothing. Everything else is degradation or a
question, and neither exits 0.

### 4.7 `decisions.json`

```json
{
  "tests": {"ran": true, "outcome": "go test ./... passed"},
  "decisions": [
    {"id": "r01-3", "action": "applied",  "note": "fixed, test added"},
    {"id": "r01-4", "action": "rejected", "note": "false positive: ctx is canceled in the defer above"},
    {"id": "r01-7", "action": "deferred", "note": "needs an interface change; separate task"}
  ]
}
```

- The author must decide every id it was given. An id with no decision gets
  `{"action":"missing"}` written by the loop and a warning in the panel.
- No decisions at all, or every decision `missing`, stops the loop — a round that changed
  nothing and recorded nothing would repeat forever. This is v1 behavior, kept.
- `rejected` and `deferred` fingerprints carry into the next round's request with their
  reasons and one instruction: these are settled, do not raise them again; if a reason is
  wrong, say so explicitly and once.

Carrying a decision journal forward is the loop's own contribution and is where it
diverges from every one-shot review command: those have no memory of what was already
argued.

---

## 5. The run archive

Ships early — nothing after it can be explained after the fact otherwise.

### 5.1 Per-run archive

Under the plugin state directory; the repository is not the place for tens of megabytes of
raw model output.

```
<state>/history/<run-id>/
├── manifest.json
├── events.jsonl
├── round-01/
│   ├── prompt-review.md      what was sent to the reviewer, verbatim
│   ├── review.raw.txt        the reviewer's output, verbatim
│   ├── review.json           parsed, with ids and fingerprints
│   ├── prompt-fix.md
│   ├── fix.raw.txt
│   ├── decisions.json
│   └── changes.patch         everything the author phase changed, new files included
└── report.json
```

`changes.patch` answers the one question nothing else answers: is the loop converging, or
moving the same lines back and forth.

It is captured through a temporary `GIT_INDEX_FILE`, the mechanism `Checkpoints` already
uses: the loop stages the tree into a throwaway index with `git add -A` — which honors
`info/exclude`, so the run directory stays out (§3.2) — and diffs the tree recorded at the
start of the author phase against it. A plain `git diff` would omit every file the author
newly created, and a new test is the most common thing an author creates, so the fix that
mattered most would be missing from the patch, from the archive and from the
`regressions_only` baseline (§6.2) at once. The user's index, stash and `HEAD` are
untouched (§2.2).

`archive.raw_output: false` drops `*.raw.txt` for users who do not want model output
retained. It wins everywhere, including over the degradation ladder (§4.5): a round whose
output never parsed still records the failure as an event and names it in the panel, but
the raw text is not retained, because a privacy setting that has exceptions is not one.
Keeping unparsable output for diagnosis means leaving `raw_output` at its default.
Everything else is always written.

### 5.2 `manifest.json`

The resolved configuration with provenance, so a month-old run can be explained: profile
name and the layer it came from; the review command and level used per round; kind, name
and pane id of author and reviewer; agent CLI versions where obtainable; the scope and its
command; `HEAD` at start; plugin version.

### 5.3 `events.jsonl`

One object per line:

```json
{"ts":"2026-08-16T19:12:04Z","round":1,"phase":"review","event":"stall","detail":"no output 5m12s"}
```

Events: `phase_start`, `phase_done`, `stall`, `retry`, `timeout`, `blocked`,
`parse_fallback`, `degraded`, `canceled`. The panel consumes this stream instead of
parsing the log, which is also how it gets stall and retry lines for free.

### 5.4 `report.json`

The run's outcome in one file: the exit code and what ended the run, the rounds it took, and
one entry per finding the reviewer ever produced — its id, fingerprint, file, category,
severity, verdict, title, and what became of it.

`action` and `note` are always present, never null, and `action` is a closed set wider than
the author's three verbs, because most findings reach an author but not all do:

| `action` | The finding | `note` |
|---|---|---|
| `applied`, `rejected`, `deferred` | was decided by the author | the author's own |
| `missing` | was sent to the author, which decided nothing about it (§4.7) | empty |
| `filtered` | was below `min_verdict` and never sent (§8) | the verdict that filtered it |
| `pre_existing` | was reported as predating the diff and never sent (§4.1) | empty |
| `unreviewed` | was raised in a round that no author phase followed — the last round, a spent budget, a stop | why the run ended |

Every finding gets exactly one entry, whatever became of it: a report that listed only the
findings someone answered would be silent about exactly the rounds that went wrong. This is
what makes `show` (§3.5) a rendering job rather than a re-derivation, and it is the file to
read when asking what a run did.

### 5.5 Rotation

`archive.keep`, default 20 runs. Older run directories are removed at run start, in the same
pass that already drops aged checkpoint refs. Nothing outlives rotation: an archive is for
explaining a run that just happened or one from last week, not a dataset. Raising `keep` is
how someone who wants more history gets it.

---

## 6. Who decides what to look for

### 6.1 The reviewer does

The agents already ship maintained review capability, with scoping, effort levels,
structured findings and their own verification pass:

| Kind | Command | Scoping | Level |
|---|---|---|---|
| `codex` | `codex review [PROMPT]` | `--uncommitted` (`--base`, `--commit` exist and are unused, §9) | no flag exists — carried in `[PROMPT]`; custom instructions as argument or stdin; verified against codex-cli 0.147.0 |
| `claude` | `/code-review [low\|medium\|high\|max]`, `/security-review` | diff, PR, branch, path | the command's own argument: `low`/`medium` narrow and high-confidence, `high`/`max` broad; `ultra` is multi-agent |
| others | built-in fallback prompt | scope from §9 | in the prompt — one prompt, not a library, the only place a taxonomy remains |

`--uncommitted` is literally the loop's default scope, so that mapping is direct rather
than negotiated, and it is the only scope flag the loop uses (§9).

The **level** is not uniformly a flag, and the adapter says so per kind rather than
pretending to a shared scale: where the CLI has one (`claude`), the loop passes it; where
it does not (`codex`), the level is expressed in the custom-instruction argument — *"broad
first pass, report anything worth a look"* versus *"narrow pass, only what you are
confident about"*. Both are the round policy talking to the reviewer; only the transport
differs. Whether a kind's level changes anything is answerable from the archive when someone
asks; it is not worth a measurement layer to find out (§2.2).

The loop therefore stops carrying a review taxonomy. This is not delegation for its own
sake: a hand-written prompt competes with, and loses to, the instruction set the reviewer
already applies — and it competes for the same context with the project's own `CLAUDE.md`
and `AGENTS.md`, which is where project-specific review standards belong (§4.4).

### 6.2 The loop owns the round policy

What no one-shot review command has is a notion of *this is round three and we are only
chasing regressions now*. That is the whole of what survives from the old lens design:

```json
"rounds": [
  {"level": "high"},
  {"level": "medium"},
  {"level": "low", "regressions_only": true}
]
```

The first entry is the broad first pass; the last entry repeats for every round beyond the
list.

Narrowing is expressed through the reviewer's native effort scale rather than through lens
lists, because that scale already means what the policy needs it to mean.

A round may also carry `command` to name a different native command for that round —
`security-review` for a hardening pass, say — and `instructions`, a short free-text block
appended to the command's custom-instruction argument.

`regressions_only: true` is likewise instructions plus data, not a mode the reviewer knows
about. The loop appends a generated block — *"restrict this pass to what the fixes in the
rounds below broke or left broken; do not raise anything that predates them"* — and names
the baseline it must be judged against: the concatenated `changes.patch` of every prior
author phase (§5.1), by absolute path, for the reviewer to read. `regression: true` in
`review.json` (§4.1) is how the reviewer marks what it found that way. On round 1 the
setting has no baseline and is ignored with a warning, which is the only sensible reading
of "regressions only" before anything has been changed.

### 6.3 Profiles and resolution layers

A profile is a JSON file with the same schema as `config.json` (§10) — the run settings a
policy wants to fix, plus `rounds`:

```json
{
  "description": "the default loop",
  "max_iterations": 10,
  "min_verdict": "confirmed",
  "rounds": [
    {"level": "high"},
    {"level": "medium"},
    {"level": "low", "regressions_only": true}
  ]
}
```

One schema and one parser for both, so a key means the same thing wherever it is written and
`config` (§11) can explain any of them the same way. Free-text guidance for the reviewer goes
in a round's `instructions` (§6.2), which is the only thing a profile body ever carried.

Layers, **highest number wins** per key: invocation beats project, project beats user, user
beats the built-in defaults.

| # | Layer | Location |
|---|---|---|
| 1 | built-in defaults | compiled in via `embed.FS` |
| 2 | user | `<herdr plugin config dir>/` |
| 3 | project | `<repo>/.review-loop/` |
| 4 | invocation | `plugin_action` args |

Resolution is **per file**, not per directory. `config.json` merges per key. Arrays —
`rounds` — are replaced whole by the layer that defines them; half-merged arrays produce
behavior nobody can debug. `review --profile <name>` selects one, defaulting to `default`;
dropping `.review-loop/profiles/release.json` in place is the entire registration procedure.

A repository with no `.review-loop/` and a user with no `config.json` get a complete run.
Absence of configuration is neither an error nor a warning.

The project layer is read from the working copy like any other file, including when the diff
under review is the one editing it. Freezing it from `HEAD` was specified and cut (§2.2): the
same branch can redirect the review through `CLAUDE.md` or `AGENTS.md`, which the reviewer
reads itself and no setting here can freeze. The README says plainly that the round policy is
a convenience, not a boundary, and that a diff touching either deserves a human's eye.

### 6.4 The per-kind adapter

One small table maps agent kind → review command, level flags, scope flags, and the shape
of its output, in the same way `reset_command` already maps kind → `/clear` or `/new`.
`review_command` in `config.json` overrides the entry for **any** kind, known or not: a
known kind whose CLI has moved is the common case, and a table this small is not worth
protecting from its user. An entry for an unknown kind is what makes that kind supported
without a release.

An adapter is expected to break when an agent CLI changes. That is why §4.5 keeps a
markdown fallback and a reformat turn, and why `parse_fallback` is an archived event: a
kind whose adapter has rotted leaves a trail of them in `events.jsonl`, so the diagnosis is
one `grep` over the archive rather than a mystery.

---

## 7. Watchdog, retries, exit codes

### 7.1 Stalls are not timeouts

`timeouts.stall`, default `5m`: no output from the pane for that long is a stall,
independent of the phase budget. Today an agent that goes silent in minute two burns the
full thirty.

### 7.2 Retries

`retries`, default 1, per phase: the number of repeat attempts *after* the first one, so
the phase is attempted `retries + 1` times in all — `0` never retries, `2` gives three
attempts. A stall, a crashed agent, or a parse failure that survived the reformat turn
resets the session and consumes one retry. The budget is per phase, not per round: each
phase of each round starts with a full one. When it is exhausted:

- **reviewer** — stop with code 5. It is the only source; there is nothing to degrade to.
- **author** — stop with code 5, because the next round would review unchanged code.

A value below `0` or above `5` is out of range: warning and the default, per §10.

Every retry is an event in `events.jsonl` and a line in the panel. Findings already raised
in the interrupted run are archived and recorded as `unreviewed` (§5.4): a run that
ends this way still leaves everything it learned.

### 7.3 Exit codes

"0 only on a clean review" is kept; dirty stops stop being one bucket.

| Code | Meaning |
|---|---|
| 0 | clean review |
| 1 | findings remain: the round budget is spent. A normal outcome, not a failure |
| 2 | tool error: configuration, no second agent, archive not writable |
| 3 | a human is needed: an agent is blocked on a question, or the reviewer returned a non-empty `open_questions` array (§4.6) |
| 4 | canceled through `stop` |
| 5 | terminal agent failure: a stall, a crash or an unrecoverable parse failure outlived the phase's retry budget (§4.5, §7.2) |

5 is separate from 2 because the two ask for different things: 2 is the user's setup and is
fixed before rerunning, 5 is the agent and is usually fixed by rerunning. The event in
`events.jsonl` says which of stall, crash or parse it was; the exit code only has to
separate "the loop broke" from "the loop worked and the code is not clean".

### 7.4 Stuck detection

If one fingerprint is raised in three consecutive rounds and the author records `applied`
each time, the author is "fixing" something the reviewer does not accept as fixed. The
loop stops with code 1 and names the disputed finding. Without this, such a pair silently
consumes the whole round budget. The disputed finding keeps its `applied` decisions in the
report (§5.4), so the disagreement is legible afterwards rather than only in the panel.

---

## 8. Verdicts instead of a verification stage

A false positive matters more in a loop than in a report: a human discards it, whereas the
author goes and "fixes" something that is not there and introduces something that is.

The v1 plan for this was a third phase — a verifier agent per round, grouped by directory,
issuing `confirmed | refined | rejected`. It is not built, because the reviewers already
run a verification pass and already label findings `CONFIRMED` versus `PLAUSIBLE`. An
entire phase collapses into one setting:

```
min_verdict: confirmed        # confirmed | plausible
```

Findings below the bar are recorded in the archive, and are not sent to the
author. `min_verdict: plausible` passes everything, which is pre-v2 behavior.

**The round verdict is resolved after filtering, not before.** A round whose findings are
all filtered out has nothing for the author to do, so there is no author phase and the
round is clean — flagged `filtered: <n>` in the panel, the archive and `report.json`, so
"clean" is never confused with "nothing survived the bar". Filtering can therefore end a
run with code 0, which is the point of the setting: `plausible`-only output is the loop
saying it found nothing it stands behind. `show` and the report keep every filtered finding,
marked `filtered` with the verdict that filtered it (§5.4), so a bar set too high is visible
in the run that suffered from it rather than silent.

If runs keep rejecting findings this filter does not explain, a verification stage becomes
justified and gets its own specification then, with those runs as evidence. Not before.

---

## 9. Scope

`review --scope <spec>`, default `worktree`.

| Spec | Reviewed | Native mapping | The author edits |
|---|---|---|---|
| `worktree` | uncommitted changes (default) | `codex review --uncommitted` | code |
| `text:<path>` | one document, whole | custom instruction | the document |

Two, because the author may only edit the working tree (§2.2: no commits, no amends, no
staging), and that makes the working tree the end of every comparison worth offering. A scope
that stopped at the index or at `HEAD` could not see the author's fixes at all — round two
would re-review a byte-identical diff and the loop could never converge — so every candidate
scope had to be redefined as "…as it now stands in the working tree", at which point it is
`worktree` wearing a different name and a longer paragraph. `staged`, `branch[:base]`,
`commits:<range>` and `paths:<glob>` are therefore all in the same place: implemented when
someone asks and can say what they would get that `worktree` does not give them (§2.2).

`text:` earns its place by being genuinely different — the reviewed thing is a whole document
rather than a diff, and the author edits that document. Its round `instructions` replace
code-review framing with document review: gaps, contradictions, unhandled cases. This is the
one place where the loop still says what to look for, because no native command covers it.

Where the native command scopes itself, the loop passes the flag rather than describing the
scope in prose. Otherwise it writes `.review-loop/run/scope.md` with a description and the
**command** that produces the diff, and the agent runs it; the diff is never inlined, or
round three carries a prompt large enough to undo what the session reset bought.

---

## 10. Configuration

```json
{
  "profile": "default",
  "scope": "worktree",
  "reviewer": { "kind": "", "name": "" },
  "max_iterations": 10,
  "min_verdict": "confirmed",
  "timeouts": { "review": "30m", "fix": "30m", "stall": "5m" },
  "retries": 1,
  "archive": { "keep": 20, "raw_output": true },
  "review_command": {},
  "reset_command": "",
  "rounds": [{ "level": "high" }]
}
```

- `review_file` is gone (§3.3). `reviewer_kind`, `reviewer_name`, `review_timeout`,
  `fix_timeout` are replaced by their nested forms with no mapping.
- This is also the profile schema (§6.3): a profile is a file of these keys under a name.
  `profile` itself is only meaningful in the user, project and invocation layers — a profile
  naming another profile is rejected with a warning rather than followed.
- `review_command` is a per-kind override of the §6.4 table, keyed by agent kind.
- Unchanged invariant: an invalid value or unknown key produces a warning and the default,
  never a failure. `stop` has to work with a broken `config.json`.
- The settings pane edits the nested schema and validates before saving.

---

## 11. Command surface

| Command | Change |
|---|---|
| `review` | gains `--profile <name>` and `--scope <spec>` |
| `review --dry-run` | also prints the resolved scope, profile, and the review command per round |
| `config` | **new** — prints resolved configuration with the winning layer per key, profiles with their paths, the chosen pair, the scope, the per-kind review commands. Runs nothing |
| `init` | **new** — copies the winning profile down into `./.review-loop/` and writes a `config.json` holding `{}` plus the keys the user is actually overriding, not the resolved state, so the user does not silently freeze today's defaults. The explanations go next to it in `.review-loop/config.example.md` — every key, its default and what it does — because the format is strict JSON and cannot carry comments (§10) |
| `show` | **new** — §3.5 |
| `stop`, `finish`, `open-panel`, `open-settings`, `open-history` | unchanged, except that `finish` no longer deletes worktree files |

---

## 12. Task plan

Ordering is not cosmetic: each stage is a precondition for the next.

**Phases 5 and 6 are the deliverable to judge v2 by.** They are the whole of §1: findings
become data, the data leaves the working tree, and a finished run has an archive that
explains it. Everything in phases 7 and 8 improves a loop that already works, so none of it
is started before phases 5–6 have been used on real work for long enough to say whether the
core was worth building. That is the same rule §2.2 applies to its cut list and §8 applies to
the verification stage, and it is written here because a long task chain with no stopping
point is how a tool ends up specified further than it is wanted.

### Phase 5 — Findings as data

**T5.1 — Run directory `feat`**
`.review-loop/run/` with per-round subdirectories, `0o700`/`0o600`; the `info/exclude`
append; the rooted preflight walk over every run-path component; removal of `review_file`,
`SummaryFile`, the mtime freshness comparison, and the "do not report findings about …"
clause in both prompts. `os.Root` stays — it now guards a plugin-owned path instead of a
configured one. §3
**Done:** a run leaves the working tree containing only code changes; `git status` is clean
of loop files; a round checkpoint diff contains no loop files; a non-directory *or a
symlink* at `.review-loop`, `.review-loop/run` or a round directory fails with a message
naming the component, including when it is planted between preflight and the phase that
writes there; a repository with a committed `.review-loop/run/` file exits 2 naming it
rather than reviewing it. Verified with codex and claude panes, which is also the sandbox-write check
from §14.

**T5.2 — Review contract `feat`** *deps: T5.1*
`review.json` schema, loop-assigned ids and fingerprints, the five-step degradation ladder
ending in §7.2's retry and code 5, contradiction resolution.
§4.1, §4.2, §4.5, §4.6
**Done:** a reviewer returning v1 markdown does not break the loop; a reviewer returning
garbage costs one reformat turn and not a round; `status: clean` with findings is dirty;
`status: findings` with an empty array never exits 0; a non-empty `open_questions` array
exits 3 with the questions named, whatever `status` says; no author phase is ever entered
with unstructured findings; fingerprints are stable across rounds for the same finding.

**T5.3 — Decision journal `feat`** *deps: T5.2*
`decisions.json`, the `missing` rule, the stop condition, carry-forward of rejected and
deferred fingerprints. §4.7
**Done:** an author that decides nothing stops the loop; a rejected finding raised again
next round arrives with its recorded reason attached.

**T5.4 — Rendering `feat`** *deps: T5.3*
One renderer producing markdown from a round; `show` writes it to stdout, the history pane
pipes it to `$PAGER`. §3.5
**Done:** `show` and the history pane produce identical text for the same round; no
markdown is written to the working tree anywhere in a run.

### Phase 6 — The archive

**T6.1 — Archive `feat`** *deps: T5.3*
Run directory layout, `manifest.json` with provenance, `events.jsonl`, `report.json` with
the closed `action` set, rotation by `archive.keep`. §5
**Done:** after a run, and without rerunning it, one can say what each agent was sent and
which findings dropped out where; at the default `archive.raw_output: true`, also what each
agent answered verbatim. With `raw_output: false` the parsed `review.json` and
`decisions.json` still answer the rest — that is the trade the setting buys (§5.1).

**T6.2 — Round patches `feat`** *deps: T6.1*
`changes.patch` per round, from the diff across the author phase. §5.1
**Done:** a run that oscillates over the same lines is visible as such from the patches
alone.

**T6.3 — Panel on events `feat`** *deps: T6.1*
The panel reads `events.jsonl` instead of parsing the log; stall and retry lines appear
there. §5.3
**Done:** the panel shows a stall within `timeouts.stall` of it starting.

### Phase 7 — Delegated review

Not started before phases 5–6 have run on real work.

**T7.1 — Layers and profiles `feat`** *deps: T5.1*
`embed.FS` defaults, per-file resolution across four layers, per-key `config.json` merge,
whole-array replacement, the shared JSON schema for `config.json` and profiles, the default
profile. §6.3, §10
**Done:** a repository with no `.review-loop/` and a user with no `config.json` runs
without a warning; a project profile overriding `rounds` replaces it whole; one decoder
reads both file kinds.

**T7.2 — Per-kind adapter `feat`** *deps: T7.1*
The kind → review command table, the capture step, `review_command` overrides, the fallback
prompt for kinds with no native command. §4.3, §6.1, §6.4
**Done:** a claude reviewer runs its own review command and writes conforming
`review.json`; the same for codex; an unknown kind falls back and still produces a valid
round. The in-pane form of each command is confirmed by hand before the table is written.

**T7.3 — Round policy `feat`** *deps: T7.2*
`rounds` drives level, command and instructions per round; the last entry repeats. §6.2
**Done:** the built-in profile reproduces the round 1 / 2 / 3+ narrowing; a two-entry
project profile behaves as specified from round three on.

**T7.4 — `config` and `init` `feat`** *deps: T7.3*
Both subcommands, `init` writing a minimal strict-JSON `config.json` beside a documented
`config.example.md`. §11
**Done:** `config` names the winning layer for every key and the review command for every
round; `init` on a fresh repository produces a `.review-loop/` that changes no behavior and
whose `config.json` the loader accepts unmodified.

**T7.5 — Verdict filter `feat`** *deps: T7.2*
`min_verdict`; filtered findings archived and recorded in the report but withheld from the
author. §8
**Done:** `min_verdict: plausible` passes everything; `confirmed` withholds the rest and
they are still visible in `show` and the report.

### Phase 8 — Resilience

**T8.1 — Stalls and retries `feat`** *deps: T6.1* — §7.1, §7.2
**Done:** a silent agent is caught within the stall budget rather than the phase budget;
`retries` attempts are made after the first and each is recorded; a reviewer that exhausts
the budget stops the run; `retries: 0` stops on the first failure.

**T8.2 — Exit codes `feat`** *deps: T8.1* — §7.3
**Done:** spent budget exits 1; a missing second agent exits 2; a blocked agent exits 3;
`stop` exits 4; a reviewer that stalls twice, and a reviewer whose output never parses,
both exit 5 rather than 0 or 1.

**T8.3 — Stuck detection `feat`** *deps: T5.3* — §7.4
**Done:** a synthetic run with a reviewer that never accepts a fix stops in round three
instead of round ten.

### Phase 9 — Optional

**T9.1 — `text:` scope `feat`** *deps: T7.3* — §9
**Done:** no argument is indistinguishable from today; `--scope text:docs/plan.md` runs a
meaningful loop over a document and converges, with the author editing the document.

### Order

T5.1 → T5.2 → T5.3 → T5.4 → T6.1 → T6.2 → T6.3 — **use it** — T7.1 → T7.2 → T7.3 → T7.4 →
T7.5 → T8.1 → T8.2 → T8.3 → T9.1.

T8.3 depends only on T5.3 and T9.1 only on T7.3, so both can move earlier if convenient.
T7.2 is the highest-risk task and can be prototyped by hand against both CLIs before T5.1
is finished.

---

## 13. Definition of done

1. A repository with no `.review-loop/` and a user with no `config.json` complete a run
   from one keypress, exactly as in v1.
2. A finished run leaves the working tree containing code changes and nothing else.
3. The reviewer's output is structured; a malformed turn costs one reformat, not a round.
4. Every finding has an id the author must decide on and a fingerprint that survives across
   the rounds of a run.
5. Any archived run can be explained after the fact: what was sent, which findings dropped
   out where, and what each round changed — plus what came back verbatim whenever
   `archive.raw_output` is at its default (§5.1); turning it off narrows this criterion to
   the parsed record by design.
6. **Nothing a run produced is lost while the run is kept.** Every finding the reviewer
   raised is in that run's `report.json` with the action taken on it, including the ones no
   author ever saw. Rotation then drops whole runs, oldest first, and nothing survives it —
   the archive explains recent runs, it is not a record of everything ever found (§2.2).
7. The loop carries no review taxonomy of its own outside the fallback prompt and the
   `text:` scope.
8. The convergence policy lives in a file a project can commit.
9. A spent budget, a terminal agent failure, a blocked agent, a cancellation and a tool
   error are distinguishable from the exit code alone; which kind of agent failure it was
   is one line of `events.jsonl` away.
10. Criteria 1–6 hold at the end of phase 6, on their own. If they hold and the loop is not
    better to use than v1, the rest of the plan is not the fix and is not built (§12).

---

## 14. Risks

- **Adapter rot.** The review commands and their output belong to other people and will
  change. Mitigated, not solved, by §4.5's fallback ladder and by `parse_fallback` being a
  recorded event rather than a surprise. This is the price of §6.1 and it is worth paying:
  the alternative is maintaining a review taxonomy, which rots faster and more quietly.
- **Sandbox writes.** The artifact design rests on agents writing inside the repository but
  outside git's view. If some kind refuses `.review-loop/run/`, the fallback is a run
  directory at the repository root with the same exclude trick — not a return to files in
  the diff. Checked in T5.1.
- **Archive growth.** Bounded by `archive.keep` and by nothing else, and raw model output is
  the bulk of it. `raw_output: false` and a lower `keep` are the two dials; both are
  documented next to the size they control (§5.1, §5.5).
- **Scope creep into an orchestrator.** Every section is judged by whether an unattended
  loop converges faster. Fan-out, model APIs and revmux are out (§2.2) and stay out.
- **Scope creep into a corpus tool.** The nearer risk, and the one this revision cut: a
  permanent findings store, measurement over it, configuration layers and extra scopes each
  look cheap on their own and together outweigh the loop they were meant to serve. The rule is §12's — nothing beyond phase 6 is built until the
  loop it improves has been used, and anything cut stays cut until a real question demands
  it, not a plausible one.
