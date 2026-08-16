package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The actions an author may record, plus the ones only the loop assigns. Together they are the
// closed set the report uses, so every finding a run produced has exactly one outcome recorded.
const (
	ActionApplied  = "applied"
	ActionRejected = "rejected"
	ActionDeferred = "deferred"
	// ActionMissing is written by the loop for an id the author was given and decided nothing about.
	ActionMissing = "missing"
	// ActionFiltered marks a finding that was below min_verdict and never sent to the author. Its
	// note is the verdict that filtered it, so a bar set too high is visible in the run that
	// suffered from it rather than silent.
	ActionFiltered = "filtered"
	// ActionPreExisting marks a finding the reviewer said predated the diff, which is never sent.
	ActionPreExisting = "pre_existing"
	// ActionUnreviewed marks a finding raised in a round that no author phase followed.
	ActionUnreviewed = "unreviewed"
)

// Tests is the author's account of whether it verified its own fixes.
type Tests struct {
	Ran     bool   `json:"ran"`
	Outcome string `json:"outcome"`
}

// Decision is what the author did about one finding.
type Decision struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Note   string `json:"note"`
}

// Decisions is the author's output for one round.
type Decisions struct {
	Tests     Tests      `json:"tests"`
	Decisions []Decision `json:"decisions"`
}

// Find returns the decision recorded for an id.
func (d Decisions) Find(id string) (Decision, bool) {
	for _, decision := range d.Decisions {
		if decision.ID == id {
			return decision, true
		}
	}
	return Decision{}, false
}

// Counts is how many of each action the author recorded, for the panel and the closing digest.
func (d Decisions) Counts() map[string]int {
	counts := map[string]int{}
	for _, decision := range d.Decisions {
		counts[decision.Action]++
	}
	return counts
}

// Decided is how many findings the author actually accounted for. An author that decides nothing
// has not done a round's work, however much JSON it wrote.
func (d Decisions) Decided() int {
	decided := 0
	for _, decision := range d.Decisions {
		if decision.Action != ActionMissing {
			decided++
		}
	}
	return decided
}

// ErrNoDecisions reports output the loop could find no decisions in, which sends the author phase
// into the same degradation ladder the reviewer's output uses.
var ErrNoDecisions = errors.New("no decisions could be read from the output")

// ParseDecisions reads the author's output and completes it against the ids it was given: an id
// with no decision gets `missing`, which is what makes a silently skipped finding visible instead
// of merely absent. Decisions for ids that were never issued are dropped and named.
func ParseDecisions(raw string, ids []string) (Decisions, []string, error) {
	object, found := lastJSONObject(stripFences(raw))
	if !found {
		return Decisions{}, nil, ErrNoDecisions
	}
	var decisions Decisions
	if err := json.Unmarshal([]byte(object), &decisions); err != nil {
		return Decisions{}, nil, fmt.Errorf("%w: %w", ErrNoDecisions, err)
	}
	issued := make(map[string]bool, len(ids))
	for _, id := range ids {
		issued[id] = true
	}
	var notes []string
	seen := map[string]bool{}
	kept := make([]Decision, 0, len(ids))
	for _, decision := range decisions.Decisions {
		decision.ID = strings.TrimSpace(decision.ID)
		decision.Action = strings.ToLower(strings.TrimSpace(decision.Action))
		switch {
		case !issued[decision.ID]:
			notes = append(notes, "degraded: decision for unknown id "+decision.ID)
			continue
		case seen[decision.ID]:
			notes = append(notes, "degraded: duplicate decision for "+decision.ID)
			continue
		case decision.Action != ActionApplied && decision.Action != ActionRejected && decision.Action != ActionDeferred:
			notes = append(notes, "degraded: unknown action "+decision.Action+" for "+decision.ID)
			continue
		}
		seen[decision.ID] = true
		kept = append(kept, decision)
	}
	for _, id := range ids {
		if !seen[id] {
			kept = append(kept, Decision{ID: id, Action: ActionMissing})
			notes = append(notes, "degraded: no decision for "+id)
		}
	}
	decisions.Decisions = kept
	return decisions, notes, nil
}

// Settled is a point the author has closed, carried into the next round's request so the reviewer
// does not relitigate it. Carrying this journal forward is where the loop diverges from every
// one-shot review command: those have no memory of what was already argued.
type Settled struct {
	Fingerprint string
	Round       int
	Action      string
	Title       string
	Note        string
}

// Journal accumulates the rejected and deferred decisions of every round so far, keyed by
// fingerprint so a finding raised again arrives with the reason it was refused attached.
type Journal struct {
	order   []string
	entries map[string]Settled
}

// Record adds a round's rejected and deferred decisions. Applied ones are deliberately not carried:
// a fix the reviewer still objects to is a live disagreement, not a settled point.
func (j *Journal) Record(round int, review Review, decisions Decisions) {
	if j.entries == nil {
		j.entries = map[string]Settled{}
	}
	for _, finding := range review.Findings {
		decision, found := decisions.Find(finding.ID)
		if !found || decision.Action != ActionRejected && decision.Action != ActionDeferred {
			continue
		}
		if _, seen := j.entries[finding.Fingerprint]; !seen {
			j.order = append(j.order, finding.Fingerprint)
		}
		j.entries[finding.Fingerprint] = Settled{
			Fingerprint: finding.Fingerprint,
			Round:       round,
			Action:      decision.Action,
			Title:       finding.Title,
			Note:        decision.Note,
		}
	}
}

// Entries lists what has been settled, oldest first.
func (j *Journal) Entries() []Settled {
	entries := make([]Settled, 0, len(j.order))
	for _, fingerprint := range j.order {
		entries = append(entries, j.entries[fingerprint])
	}
	return entries
}
