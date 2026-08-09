package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadRejectsBadValuesAndKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(`{"max_iterations":1001,"review_file":"../secrets","review_timeout":"no","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	values, warnings := Load(dir)
	if values != Defaults() {
		t.Fatalf("got %#v, want defaults %#v", values, Defaults())
	}
	if len(warnings) != 4 {
		t.Fatalf("got warnings %#v", warnings)
	}
}
func TestSaveStoresOnlyChoices(t *testing.T) {
	dir := t.TempDir()
	values := Defaults()
	values.MaxIterations = 2
	values.FixTimeout = time.Minute
	path, err := Save(dir, values)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, `"max_iterations": 2`) || !strings.Contains(got, `"fix_timeout": "1m0s"`) || strings.Contains(got, "review_file") {
		t.Fatalf("unexpected config %s", got)
	}
	loaded, warnings := Load(dir)
	if len(warnings) != 0 || loaded != values {
		t.Fatalf("round trip: %#v %#v", loaded, warnings)
	}
}
func TestParse(t *testing.T) {
	if _, err := Parse("review_file", "../review.md"); err == nil {
		t.Fatal("accepted escaping path")
	}
	got, err := Parse("review_timeout", "90s")
	if err != nil || got != 90*time.Second {
		t.Fatalf("got %#v %v", got, err)
	}
	if _, err := Parse("scope", " "); err == nil {
		t.Fatal("accepted blank scope")
	}
}
