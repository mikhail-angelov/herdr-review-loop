package loop

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseVerdict(t *testing.T) {
	cases := map[string]Verdict{"STATUS: CLEAN\n": Clean, "\n status: clean\n": Clean, "STATUS: CLEAN, except": Findings, "STATUS: FINDINGS": Findings, "": Findings}
	for input, want := range cases {
		if got := ParseVerdict(input); got != want {
			t.Errorf("ParseVerdict(%q) = %v, want %v", input, got, want)
		}
	}
}
func TestReviewFileRootRejectsPostValidationSymlinkSwap(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "review.md"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "review.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	review, err := OpenReviewFile(repo, "sub/review.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = review.Close() }()
	if err := os.Remove(filepath.Join(sub, "review.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sub); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, sub); err != nil {
		t.Fatal(err)
	}
	if err := review.Remove(); err == nil {
		t.Fatal("remove escaped root")
	}
	contents, err := os.ReadFile(filepath.Join(outside, "review.md"))
	if err != nil || string(contents) != "outside" {
		t.Fatalf("outside file changed: %q %v", contents, err)
	}
}
func TestWrittenSince(t *testing.T) {
	repo := t.TempDir()
	review, err := OpenReviewFile(repo, "review.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = review.Close() }()
	if err := os.WriteFile(filepath.Join(repo, "review.md"), []byte("STATUS: CLEAN"), 0o644); err != nil {
		t.Fatal(err)
	}
	contents, fresh, err := review.WrittenSince(time.Now().Add(-time.Second))
	if err != nil || !fresh || contents == "" {
		t.Fatalf("got %q %v %v", contents, fresh, err)
	}
}
