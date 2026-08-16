package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestResolveRejectsBadValuesAndKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	settings := `{"max_iterations":1001,"retries":9,"timeouts":{"review":"no"},"archive":{"raw_output":"yes"},"unknown":true}`
	if err := os.WriteFile(Path(dir), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	resolution := Resolve(Sources{User: dir})
	if !Same(resolution.Values, Defaults()) {
		t.Fatalf("got %#v, want defaults %#v", resolution.Values, Defaults())
	}
	if len(resolution.Warnings) != 5 {
		t.Fatalf("got warnings %#v", resolution.Warnings)
	}
}

func TestResolveReadsNestedObjects(t *testing.T) {
	dir := t.TempDir()
	settings := `{"reviewer":{"kind":"claude"},"timeouts":{"review":"90s","fix":"1m"},"archive":{"keep":3,"raw_output":false},"retries":0}`
	if err := os.WriteFile(Path(dir), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	resolution := Resolve(Sources{User: dir})
	if len(resolution.Warnings) != 0 {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
	want := Defaults()
	want.Reviewer.Kind = "claude"
	want.Timeouts = Timeouts{Review: 90 * time.Second, Fix: time.Minute, Stall: want.Timeouts.Stall}
	want.Archive = Archive{Keep: 3, RawOutput: false}
	want.Retries = 0
	if !Same(resolution.Values, want) {
		t.Fatalf("got %#v, want %#v", resolution.Values, want)
	}
	if resolution.Layers["reviewer.kind"] != LayerUser || resolution.Layers["max_iterations"] != LayerDefault {
		t.Fatalf("layers %#v", resolution.Layers)
	}
}

func TestResolveWarnsAboutAnUnknownMemberOfAKnownGroup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(`{"archive":{"forever":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolution := Resolve(Sources{User: dir})
	if !Same(resolution.Values, Defaults()) {
		t.Fatalf("got %#v, want defaults", resolution.Values)
	}
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], "archive.forever") {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
}

func TestSaveStoresOnlyChoicesAndNestsThem(t *testing.T) {
	dir := t.TempDir()
	values := Defaults()
	values.MaxIterations = 2
	values.Timeouts.Fix = time.Minute
	values.Archive.RawOutput = false
	path, err := Save(dir, values)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{`"max_iterations": 2`, `"timeouts"`, `"fix": "1m"`, `"raw_output": false`} {
		if !strings.Contains(got, want) {
			t.Fatalf("config %s is missing %s", got, want)
		}
	}
	for _, unwanted := range []string{"review_file", `"review":`, `"keep"`} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("config %s stores a default as %s", got, unwanted)
		}
	}
	resolution := Resolve(Sources{User: dir})
	if len(resolution.Warnings) != 0 || !Same(resolution.Values, values) {
		t.Fatalf("round trip: %#v %#v", resolution.Values, resolution.Warnings)
	}
}

func TestParseAcceptsEveryFieldAndRejectsInvalidValues(t *testing.T) {
	valid := map[string]string{
		"profile":            "release",
		"scope":              "text:docs/plan.md",
		"reviewer.kind":      "claude",
		"reviewer.name":      "pane-1",
		"max_iterations":     "2",
		"min_verdict":        "plausible",
		"timeouts.review":    "90s",
		"timeouts.fix":       "1m",
		"timeouts.stall":     "30s",
		"retries":            "0",
		"archive.keep":       "5",
		"archive.raw_output": "false",
		"reset_command":      "/new",
	}
	for _, field := range Fields() {
		if _, err := Parse(field.Key, valid[field.Key]); err != nil {
			t.Errorf("%s: rejected valid value: %v", field.Key, err)
		}
	}
	empty := []string{"max_iterations", "min_verdict", "scope", "timeouts.review", "timeouts.fix", "timeouts.stall", "retries", "archive.keep", "archive.raw_output"}
	for _, key := range empty {
		if _, err := Parse(key, ""); err == nil {
			t.Errorf("%s: accepted empty invalid value", key)
		}
	}
	if _, err := Parse("retries", "6"); err == nil {
		t.Error("retries accepted a value above the range")
	}
	if _, err := Parse("profile", "../escape"); err == nil {
		t.Error("profile accepted a path")
	}
	if _, err := Parse("scope", "branch"); err == nil {
		t.Error("scope accepted an unsupported spec")
	}
}

func TestParseAndApplyRoundTripEveryField(t *testing.T) {
	values := Defaults()
	for _, field := range Fields() {
		parsed, err := Parse(field.Key, Show(field.Key, values))
		if err != nil {
			t.Fatalf("%s: %v", field.Key, err)
		}
		if err := Apply(&values, field.Key, parsed); err != nil {
			t.Fatalf("%s: %v", field.Key, err)
		}
	}
	if !Same(values, Defaults()) {
		t.Fatalf("round trip changed %#v", values)
	}
}
