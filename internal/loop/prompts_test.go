package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptsMatchGoldenFixtures(t *testing.T) {
	for name, got := range map[string]string{
		"review-round-1.txt": ReviewPrompt("/repo/review.md", "/repo/review-summary.md", 1, 3),
		"review-round-3.txt": ReviewPrompt("/repo/review.md", "/repo/review-summary.md", 3, 3),
		"fix.txt":            FixPrompt("/repo/review.md", "/repo/review-summary.md", 2),
	} {
		want, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if got != strings.TrimSuffix(string(want), "\n") {
			t.Errorf("%s differs from golden fixture", name)
		}
	}
}

func TestFixPromptTightensAfterEachRound(t *testing.T) {
	first := FixPrompt("review.md", "summary.md", 1)
	third := FixPrompt("review.md", "summary.md", 3)
	if !strings.Contains(first, "medium-severity") {
		t.Fatalf("first-round policy missing medium findings: %q", first)
	}
	if !strings.Contains(third, "only high-severity findings") {
		t.Fatalf("later-round policy was not tightened: %q", third)
	}
}
