package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptsMatchGoldenFixtures(t *testing.T) {
	for name, got := range map[string]string{
		"review-round-1.txt": ReviewPrompt("the scope", "/repo/review.md", 1, 3),
		"review-round-3.txt": ReviewPrompt("the scope", "/repo/review.md", 3, 3),
		"fix.txt":            FixPrompt("/repo/review.md"),
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
