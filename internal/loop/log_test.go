package loop

import (
	"os"
	"strings"
	"testing"
)

func TestLogTailPhaseOutcomeAndArchive(t *testing.T) {
	log := Log{StateDir: t.TempDir()}
	for range 2000 {
		if err := log.Write(strings.Repeat("x", 10)); err != nil {
			t.Fatal(err)
		}
	}
	if err := log.Write("--- iteration 2/3: review"); err != nil {
		t.Fatal(err)
	}
	if err := log.Write("clean after 2 iteration(s)"); err != nil {
		t.Fatal(err)
	}
	tail, err := log.Tail()
	if err != nil || len(tail) > 8192 || Phase(tail) != "2/3 reviewing" || LastOutcome(tail) == "" {
		t.Fatalf("tail=%d phase=%q outcome=%q err=%v", len(tail), Phase(tail), LastOutcome(tail), err)
	}
	if err = log.Archive("run", 1, "STATUS: CLEAN"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(log.StateDir + "/history/run/iteration-01.md")
	if err != nil || string(contents) != "STATUS: CLEAN" {
		t.Fatalf("archive %q %v", contents, err)
	}
}
