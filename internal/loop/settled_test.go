package loop

import (
	"strings"
	"testing"
)

func TestSettledPointsAreCarriedToTheNextRunAndDroppedAtFinish(t *testing.T) {
	state := t.TempDir()
	review := Review{Findings: []Finding{{ID: "r01-1", Fingerprint: "fp1", Title: "log less"}}}
	decisions := Decisions{Decisions: []Decision{{ID: "r01-1", Action: ActionRejected, Note: "the logging is deliberate"}}}
	var journal Journal
	journal.Record(1, review, decisions)
	if err := SaveSettled(state, "/repo", journal.Entries()); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettled(state, "/elsewhere"); got != nil {
		t.Fatalf("another repository sees %#v", got)
	}
	carried := LoadSettled(state, "/repo")
	if len(carried) != 1 || carried[0].Note != "the logging is deliberate" {
		t.Fatalf("carried %#v", carried)
	}
	var next Journal
	next.Restore(carried)
	if block := settledBlock(next.Entries()); !strings.Contains(block, "the logging is deliberate") {
		t.Fatalf("the reviewer is not told what was settled: %q", block)
	}
	ClearSettled(state, "/repo")
	if got := LoadSettled(state, "/repo"); got != nil {
		t.Fatalf("finishing left %#v behind", got)
	}
}

func TestOneRepositorySettlingDoesNotEvictAnother(t *testing.T) {
	state := t.TempDir()
	here := []Settled{{Fingerprint: "fp1", Round: 1, Action: ActionRejected, Note: "deliberate here"}}
	there := []Settled{{Fingerprint: "fp2", Round: 1, Action: ActionDeferred, Note: "deferred there"}}
	if err := SaveSettled(state, "/here", here); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettled(state, "/there", there); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettled(state, "/here"); len(got) != 1 || got[0].Note != "deliberate here" {
		t.Fatalf("the second repository overwrote the first: %#v", got)
	}
	ClearSettled(state, "/here")
	if got := LoadSettled(state, "/here"); got != nil {
		t.Fatalf("finishing left %#v behind", got)
	}
	if got := LoadSettled(state, "/there"); len(got) != 1 || got[0].Note != "deferred there" {
		t.Fatalf("finishing one repository cleared another: %#v", got)
	}
}

func TestRestoredPointsAreNotDuplicatedByTheRoundThatSettlesThemAgain(t *testing.T) {
	var journal Journal
	journal.Restore([]Settled{{Fingerprint: "fp1", Round: 1, Action: ActionRejected, Title: "log less"}})
	journal.Record(2, Review{Findings: []Finding{{ID: "r02-1", Fingerprint: "fp1", Title: "log less"}}},
		Decisions{Decisions: []Decision{{ID: "r02-1", Action: ActionDeferred, Note: "next release"}}})
	entries := journal.Entries()
	if len(entries) != 1 || entries[0].Action != ActionDeferred || entries[0].Round != 2 {
		t.Fatalf("entries %#v", entries)
	}
}
