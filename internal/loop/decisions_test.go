package loop

import (
	"errors"
	"strings"
	"testing"
)

func TestParseDecisionsCompletesWhatTheAuthorLeftOut(t *testing.T) {
	raw := `{"tests":{"ran":true,"outcome":"go test ./... passed"},
	  "decisions":[
	    {"id":"r01-1","action":"Applied","note":"fixed"},
	    {"id":"r01-1","action":"rejected","note":"twice"},
	    {"id":"r01-9","action":"applied","note":"never issued"},
	    {"id":"r01-2","action":"maybe","note":"not a verb"}
	  ]}`
	decisions, notes, err := ParseDecisions(raw, []string{"r01-1", "r01-2", "r01-3"})
	if err != nil {
		t.Fatal(err)
	}
	if !decisions.Tests.Ran || decisions.Tests.Outcome == "" {
		t.Fatalf("tests %#v", decisions.Tests)
	}
	if len(decisions.Decisions) != 3 {
		t.Fatalf("decisions %#v", decisions.Decisions)
	}
	first, _ := decisions.Find("r01-1")
	if first.Action != ActionApplied {
		t.Fatalf("first decision %#v", first)
	}
	for _, id := range []string{"r01-2", "r01-3"} {
		decision, found := decisions.Find(id)
		if !found || decision.Action != ActionMissing {
			t.Fatalf("%s: %#v", id, decision)
		}
	}
	if decisions.Decided() != 1 {
		t.Fatalf("decided %d, want 1", decisions.Decided())
	}
	joined := strings.Join(notes, ",")
	for _, want := range []string{"duplicate decision", "unknown id r01-9", "unknown action maybe", "no decision for r01-3"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("notes %#v are missing %q", notes, want)
		}
	}
}

func TestParseDecisionsRejectsOutputWithNoObject(t *testing.T) {
	if _, _, err := ParseDecisions("I applied everything.", []string{"r01-1"}); !errors.Is(err, ErrNoDecisions) {
		t.Fatalf("got %v, want ErrNoDecisions", err)
	}
}

func TestJournalCarriesRefusalsForwardAndNotFixes(t *testing.T) {
	review := Review{Findings: []Finding{
		{File: "a.go", Title: "applied one"},
		{File: "b.go", Title: "rejected one"},
		{File: "c.go", Title: "deferred one"},
	}}
	review.Identify(1)
	decisions := Decisions{Decisions: []Decision{
		{ID: "r01-1", Action: ActionApplied, Note: "fixed"},
		{ID: "r01-2", Action: ActionRejected, Note: "ctx is canceled in the defer above"},
		{ID: "r01-3", Action: ActionDeferred, Note: "needs an interface change"},
	}}
	var journal Journal
	journal.Record(1, review, decisions)
	entries := journal.Entries()
	if len(entries) != 2 {
		t.Fatalf("entries %#v", entries)
	}
	if entries[0].Action != ActionRejected || entries[0].Note == "" || entries[0].Round != 1 {
		t.Fatalf("first entry %#v", entries[0])
	}
	block := settledBlock(entries)
	if !strings.Contains(block, "rejected one") || !strings.Contains(block, "ctx is canceled") {
		t.Fatalf("settled block %q", block)
	}
	if strings.Contains(block, "applied one") {
		t.Fatalf("an applied finding was carried forward as settled: %q", block)
	}
}
