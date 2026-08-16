STATUS: FINDINGS

- [high] docs/SPEC-v2.md:168 — The new freshness rule requires the review directory (and, for the author, `decisions.json`) to be absent at phase start, but §4.5 and §7.2 retry the same review or author phase after a parse failure, stall, or crash; the prior attempt has already created those paths, so every retry fails freshness or overwrites unarchived state — define retry cleanup/archival and recreation of the failed phase's output, or give every attempt a distinct directory.
