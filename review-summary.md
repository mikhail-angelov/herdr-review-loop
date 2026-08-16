# Author decisions — rounds 1–4

Scope under review is one uncommitted document, `docs/SPEC-v2.md`. Every decision below is an
edit to that spec; no Go code is touched by this change.

- applied (r4): §3.3 scoped the "must not exist" rule to the round's *review* phase and named the
  author phase as the deliberate exception that reuses that directory; §3.4 restated freshness per
  phase — the review phase creates `round-NN`, the author phase creates nothing, reads `review.json`
  from it and requires `decisions.json` absent. Before this, §3.3 and §3.4/§3.1 contradicted each
  other and every author phase would have failed freshness or lost its review input.
- applied (r3): §13.9 item 5 and T6.1's done-criteria qualify the "what came back verbatim"
  guarantee with `archive.raw_output` and name the parsed `review.json` / `decisions.json` as what
  remains when it is off — round 2's privacy-wins fix in §5.1 had otherwise left acceptance criteria
  demanding raw text a documented configuration cannot produce.
- applied: §4.1 gives `open_questions` a schema (`question` required, `file`/`line` optional,
  malformed entries dropped as `degraded: open-question`); §4.6 routes a non-empty array to exit
  code 3 before any author phase, whatever `status` says, findings entering the corpus as
  `unreviewed`. §7.3 and T5.2 updated.
- applied: §3.2 states `info/exclude` cannot hide tracked paths, and preflight refuses to start
  (code 2) when anything under `.review-loop/run/` is tracked, naming the paths and the
  `git rm -r --cached` fix. §3.3 and T5.1 updated.
- applied: §5.1 captures `changes.patch` through a temporary `GIT_INDEX_FILE` with `git add -A`, so
  newly created files reach the patch, the archive and the `regressions_only` baseline.
- applied: §7.2 defines `retries` as repeat attempts after the first (`retries + 1` attempts, per
  phase, `0` stops on first failure) with an out-of-range rule; §4.5 steps 4–5, §4.6, §7.3 and T8.1
  reworded to the budget.
- applied: §5.1 resolves the `archive.raw_output` contradiction in favor of the privacy setting: a
  failed round records the event and names it in the panel but retains no raw text.
- applied: §3.3 requires every run-path component created, opened and read through one repository
  `os.Root`, rejecting symlinks and non-directories.
- applied: §4.6 `status: findings` with an empty array is not a clean verdict; it enters the §4.5
  ladder and can never produce exit 0.
- applied: §6.5 freezes the whole project layer (`config.json` and profiles) from `HEAD` when the
  diff touches `.review-loop/`, with per-file fallbacks recorded as manifest provenance.
- applied: §9 `staged` and `branch` compare to the working tree so later rounds see the author's
  uncommitted fixes; `--base` kept for round 1 on a clean tree.
- applied: §4.5's ladder ends in §7.2's retry and then exit 5 — one failure state machine, and no
  author phase is entered without structured findings.
- applied: §7.3 adds exit code 5 for terminal agent failure, distinct from 2; §13.9 reworded.
- applied: §8 resolves the round verdict after `min_verdict` filtering; an all-filtered round is
  clean with `filtered: <n>`, and filtered findings stay in `show`, the corpus and `stats`.
- applied: §5.6 gives `action` a closed set — `missing`, `filtered`, `pre_existing`, `unreviewed` —
  so every finding yields exactly one corpus line.
- applied: §6.2 defines `regressions_only` as generated instructions plus a named baseline (prior
  rounds' concatenated `changes.patch` by absolute path); ignored with a warning on round 1.
- applied: §6.1 corrects the codex row against codex-cli 0.147.0 — no level flag, so the level
  travels in `[PROMPT]`; the level column is per kind.
- applied: §11 `init` writes strict-JSON `config.json` with explanations in `config.example.md`.
- applied: §6.3 precedence is "highest number wins" (invocation > project > user > defaults), and
  §6.4 `review_command` overrides any kind, known or unknown.
- rejected: nothing across four rounds — every finding named a real internal contradiction, and
  none was raised a second time after being settled.

tests: `make build` and `make test` both pass (five packages ok, 55.5% total coverage); the change
is documentation only and adds no Go code, so no new tests were written.
