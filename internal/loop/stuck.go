package loop

// stuckRounds is how many consecutive rounds a finding may be raised and "fixed" before the loop
// calls the pair stuck. Two is an ordinary disagreement — the reviewer was not convinced and the
// author tried again. Three is a pair that will keep going until the budget is gone.
const stuckRounds = 3

// Disputes tracks findings the author keeps applying and the reviewer keeps raising. Without this,
// such a pair silently consumes the whole round budget while appearing to make progress.
type Disputes struct {
	// runs counts consecutive rounds per fingerprint; last is the round each was counted in, so a
	// finding that skips a round starts over rather than accumulating across a gap.
	runs map[string]int
	last map[string]int
	// stuck is the first finding to reach the limit, held rather than searched for, so which one
	// the run names does not depend on map iteration order.
	stuck string
}

// Record adds one round's findings and what the author decided about them. Only applied decisions
// extend a run: a rejected or deferred finding is an argument the journal already carries forward.
func (d *Disputes) Record(round int, review Review, decisions Decisions) {
	if d.runs == nil {
		d.runs, d.last = map[string]int{}, map[string]int{}
	}
	for _, finding := range review.Findings {
		decision, found := decisions.Find(finding.ID)
		if !found || decision.Action != ActionApplied {
			delete(d.runs, finding.Fingerprint)
			continue
		}
		if d.last[finding.Fingerprint] == round-1 {
			d.runs[finding.Fingerprint]++
		} else {
			d.runs[finding.Fingerprint] = 1
		}
		d.last[finding.Fingerprint] = round
		if d.runs[finding.Fingerprint] >= stuckRounds && d.stuck == "" {
			d.stuck = finding.Title
		}
	}
}

// Stuck names the finding the pair is deadlocked over, if there is one. The disputed finding keeps
// its applied decisions in the report, so the disagreement is legible afterwards and not only in
// the panel.
func (d *Disputes) Stuck() (title string, stuck bool) { return d.stuck, d.stuck != "" }
