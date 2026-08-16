package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLayer puts one settings file into a layer directory and returns that directory.
func writeLayer(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuiltinDefaultsDecodeCleanly(t *testing.T) {
	values := Defaults()
	if values.Profile != "default" || values.Scope != ScopeWorktree || values.MinVerdict != VerdictConfirmed {
		t.Fatalf("built-in defaults %#v", values)
	}
	if values.MaxIterations != 10 || values.Retries != 1 || values.Archive.Keep != 20 || !values.Archive.RawOutput {
		t.Fatalf("built-in defaults %#v", values)
	}
	if values.Timeouts.Stall == 0 || values.Timeouts.Review == 0 || values.Timeouts.Fix == 0 {
		t.Fatalf("built-in timeouts %#v", values.Timeouts)
	}
	if len(values.Rounds) != 1 || values.Rounds[0].Level != "high" {
		t.Fatalf("built-in rounds %#v", values.Rounds)
	}
}

func TestAbsentConfigurationIsNotAWarning(t *testing.T) {
	resolution := Resolve(Sources{User: t.TempDir(), Project: filepath.Join(t.TempDir(), ".review-loop")})
	if len(resolution.Warnings) != 0 {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
	if resolution.Profile.Name != "default" || resolution.Profile.Layer != LayerDefault {
		t.Fatalf("profile %#v", resolution.Profile)
	}
	if len(resolution.Values.Rounds) != 3 {
		t.Fatalf("the built-in default profile should supply the narrowing rounds, got %#v", resolution.Values.Rounds)
	}
}

func TestBuiltInDefaultProfileNarrowsAcrossRounds(t *testing.T) {
	rounds := Resolve(Sources{}).Values.Rounds
	want := []Round{{Level: "high"}, {Level: "medium"}, {Level: "low", RegressionsOnly: true}}
	if len(rounds) != len(want) {
		t.Fatalf("got %#v, want %#v", rounds, want)
	}
	for index, round := range rounds {
		if round != want[index] {
			t.Fatalf("round %d: got %#v, want %#v", index+1, round, want[index])
		}
	}
}

func TestHigherLayersWinPerKey(t *testing.T) {
	user := writeLayer(t, t.TempDir(), "config.json", `{"max_iterations":3,"retries":2,"reviewer":{"kind":"codex"}}`)
	project := writeLayer(t, t.TempDir(), "config.json", `{"max_iterations":5}`)
	resolution := Resolve(Sources{User: user, Project: project, Invocation: map[string]any{"scope": "text:docs/plan.md"}})
	if len(resolution.Warnings) != 0 {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
	values := resolution.Values
	if values.MaxIterations != 5 || values.Retries != 2 || values.Reviewer.Kind != "codex" || values.Scope != "text:docs/plan.md" {
		t.Fatalf("resolved %#v", values)
	}
	for key, want := range map[string]string{"max_iterations": LayerProject, "retries": LayerUser, "scope": LayerInvocation, "archive.keep": LayerDefault} {
		if resolution.Layers[key] != want {
			t.Fatalf("%s came from %s, want %s", key, resolution.Layers[key], want)
		}
	}
}

func TestAProjectProfileReplacesRoundsWhole(t *testing.T) {
	project := writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "release.json"), `{"description":"release pass","rounds":[{"level":"max"},{"level":"medium","instructions":"only the public API"}]}`)
	resolution := Resolve(Sources{Project: project, Invocation: map[string]any{"profile": "release"}})
	if len(resolution.Warnings) != 0 {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
	if resolution.Profile.Name != "release" || resolution.Profile.Layer != LayerProject || resolution.Profile.Description != "release pass" {
		t.Fatalf("profile %#v", resolution.Profile)
	}
	rounds := resolution.Values.Rounds
	if len(rounds) != 2 || rounds[0].Level != "max" || rounds[1].Instructions != "only the public API" {
		t.Fatalf("rounds %#v, the array should be replaced whole rather than merged", rounds)
	}
}

func TestAProjectProfileIsPickedOverAUserProfileOfTheSameName(t *testing.T) {
	user := writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "default.json"), `{"rounds":[{"level":"low"}],"max_iterations":2}`)
	project := writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "default.json"), `{"rounds":[{"level":"max"}]}`)
	resolution := Resolve(Sources{User: user, Project: project})
	if resolution.Profile.Layer != LayerProject {
		t.Fatalf("profile %#v", resolution.Profile)
	}
	if len(resolution.Values.Rounds) != 1 || resolution.Values.Rounds[0].Level != "max" {
		t.Fatalf("rounds %#v", resolution.Values.Rounds)
	}
	if resolution.Values.MaxIterations != Defaults().MaxIterations {
		t.Fatalf("the user profile should not contribute a key once the project has the file: %#v", resolution.Values)
	}
}

func TestAHandWrittenSettingOutranksTheProfileThatRestatesIt(t *testing.T) {
	user := t.TempDir()
	writeLayer(t, user, "config.json", `{"max_iterations":4,"profile":"strict"}`)
	writeLayer(t, user, filepath.Join(ProfileSubdir, "strict.json"), `{"max_iterations":9,"min_verdict":"plausible"}`)
	resolution := Resolve(Sources{User: user})
	if resolution.Values.MaxIterations != 4 {
		t.Fatalf("the user's own config.json should win over the profile it selected: %#v", resolution.Values)
	}
	if resolution.Values.MinVerdict != VerdictPlausible {
		t.Fatalf("the profile should still supply the keys config.json does not name: %#v", resolution.Values)
	}
	if resolution.Layers["min_verdict"] != LayerProfile || resolution.Layers["max_iterations"] != LayerUser {
		t.Fatalf("layers %#v", resolution.Layers)
	}
}

func TestAProfileCannotSelectAnotherProfile(t *testing.T) {
	user := writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "default.json"), `{"profile":"other","rounds":[{"level":"low"}]}`)
	resolution := Resolve(Sources{User: user})
	if resolution.Profile.Name != "default" {
		t.Fatalf("a profile chain was followed: %#v", resolution.Profile)
	}
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], "cannot select another profile") {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
}

func TestAMissingProfileWarnsAndTheRunStillResolves(t *testing.T) {
	resolution := Resolve(Sources{User: t.TempDir(), Invocation: map[string]any{"profile": "absent"}})
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], `"absent"`) {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
	if len(resolution.Values.Rounds) != 1 || resolution.Values.Rounds[0].Level != "high" {
		t.Fatalf("rounds %#v", resolution.Values.Rounds)
	}
}

func TestOneDecoderReadsBothFileKinds(t *testing.T) {
	// the same object is a valid config.json and a valid profile; only the file name differs.
	body := `{"retries":0,"review_command":{"gemini":"/review"},"rounds":[{"level":"low","regressions_only":true}]}`
	asConfig := Resolve(Sources{User: writeLayer(t, t.TempDir(), "config.json", body)})
	asProfile := Resolve(Sources{User: writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "default.json"), body)})
	for _, resolution := range []Resolution{asConfig, asProfile} {
		if len(resolution.Warnings) != 0 {
			t.Fatalf("warnings %#v", resolution.Warnings)
		}
		if resolution.Values.Retries != 0 || resolution.Values.ReviewCommand["gemini"] != "/review" {
			t.Fatalf("values %#v", resolution.Values)
		}
		if len(resolution.Values.Rounds) != 1 || !resolution.Values.Rounds[0].RegressionsOnly {
			t.Fatalf("rounds %#v", resolution.Values.Rounds)
		}
	}
}

func TestAMalformedRoundPolicyLeavesTheLayerBelowStanding(t *testing.T) {
	user := writeLayer(t, t.TempDir(), "config.json", `{"rounds":[{"level":"high","lens":"security"}]}`)
	resolution := Resolve(Sources{User: user})
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], "round 1") {
		t.Fatalf("warnings %#v", resolution.Warnings)
	}
	if len(resolution.Values.Rounds) != 3 {
		t.Fatalf("a rejected policy should leave the default profile's rounds in place: %#v", resolution.Values.Rounds)
	}
}

func TestAnUnreadableProfileWarnsEvenWhenALowerOneStandsIn(t *testing.T) {
	// the project profile is the one the user selected; falling through to the user's copy of the
	// name without saying so would run a policy nobody chose, silently
	user := writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "default.json"), `{"rounds":[{"level":"low"}]}`)
	project := writeLayer(t, t.TempDir(), filepath.Join(ProfileSubdir, "default.json"), `{"rounds":[`)
	resolution := Resolve(Sources{User: user, Project: project})
	if resolution.Profile.Layer != LayerUser {
		t.Fatalf("profile %#v, want the readable layer below", resolution.Profile)
	}
	if len(resolution.Warnings) != 1 || !strings.Contains(resolution.Warnings[0], "not a JSON object") {
		t.Fatalf("warnings %#v, want the unreadable project profile named", resolution.Warnings)
	}
	if len(resolution.Values.Rounds) != 1 || resolution.Values.Rounds[0].Level != "low" {
		t.Fatalf("rounds %#v", resolution.Values.Rounds)
	}
}

func TestSaveKeepsStructuredSettingsThePaneCannotEdit(t *testing.T) {
	dir := writeLayer(t, t.TempDir(), "config.json", `{"rounds":[{"level":"low"}],"review_command":{"codex":"review"},"retries":3}`)
	values := Resolve(Sources{User: dir}).Values
	values.Retries = 0
	if _, err := Save(dir, values); err != nil {
		t.Fatal(err)
	}
	resolution := Resolve(Sources{User: dir})
	if len(resolution.Values.Rounds) != 1 || resolution.Values.Rounds[0].Level != "low" {
		t.Fatalf("saving from the settings pane dropped the round policy: %#v", resolution.Values.Rounds)
	}
	if resolution.Values.ReviewCommand["codex"] != "review" || resolution.Values.Retries != 0 {
		t.Fatalf("values %#v", resolution.Values)
	}
}
