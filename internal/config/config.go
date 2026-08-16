// Package config loads and validates the review-loop configuration.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Reviewer selects the second agent: by name when one is given, otherwise by kind.
type Reviewer struct{ Kind, Name string }

// Timeouts are the per-phase budgets a round is allowed to spend.
type Timeouts struct{ Review, Fix time.Duration }

// Archive bounds what finished runs leave behind in the plugin state directory.
type Archive struct {
	Keep      int
	RawOutput bool
}

// Values is the effective configuration: file settings merged over the defaults. Every field is
// comparable, which is what lets the settings pane tell an edited copy from the one it loaded.
type Values struct {
	Reviewer      Reviewer
	MaxIterations int
	Timeouts      Timeouts
	Retries       int
	Archive       Archive
	ResetCommand  string
}

// Defaults are the settings used when config.json is absent or a setting is unreadable.
func Defaults() Values {
	return Values{
		MaxIterations: 10,
		Timeouts:      Timeouts{Review: 30 * time.Minute, Fix: 30 * time.Minute},
		Retries:       1,
		Archive:       Archive{Keep: 20, RawOutput: true},
	}
}

// Field describes one setting for the settings pane: its key, how to label it and how to read it.
type Field struct {
	Key, Label, Hint string
	Kind             string
}

// Fields lists every setting, in the order the settings pane shows them. A key containing a dot
// names a member of the nested object of the same name in config.json.
func Fields() []Field {
	return []Field{
		{"reviewer.kind", "reviewer kind", "agent kind that reviews; empty uses another kind", "optional"},
		{"reviewer.name", "reviewer name", "agent name or pane id; wins over reviewer kind", "optional"},
		{"max_iterations", "max iterations", "review/apply rounds before giving up", "count"},
		{"timeouts.review", "review timeout", "budget for one review round", "duration"},
		{"timeouts.fix", "fix timeout", "budget for applying a review", "duration"},
		{"retries", "retries", "repeat attempts per phase after the first, 0 to 5", "count"},
		{"archive.keep", "archive keep", "how many finished runs keep their archive", "count"},
		{"archive.raw_output", "archive raw output", "keep verbatim agent output in the archive", "bool"},
		{"reset_command", "reset command", "fallback reset command for unknown agent kinds", "optional"},
	}
}

// groups are the nested objects config.json carries, so a dotted field key and a JSON object stay
// two views of one setting rather than two settings.
var groups = []string{"reviewer", "timeouts", "archive"}

// Path is the config file inside the plugin's config directory.
func Path(dir string) string { return filepath.Join(dir, "config.json") }

// Load reads config.json over the defaults and returns the settings it could not use as warnings.
// A malformed user setting never prevents commands such as stop from running.
func Load(dir string) (values Values, warnings []string) {
	values = Defaults()
	data, err := os.ReadFile(Path(dir))
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return values, []string{fmt.Sprintf("cannot read config.json: %v, using defaults", err)}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return values, []string{"config.json is not a JSON object of settings, using defaults"}
	}
	known := map[string]bool{}
	for _, field := range Fields() {
		known[field.Key] = true
	}
	for key, value := range flatten(raw) {
		if !known[key] {
			warnings = append(warnings, key+" is not a herdr-review-loop setting, ignoring it")
			continue
		}
		if err := setJSON(&values, key, value); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v, using default", key, err))
		}
	}
	return values, warnings
}

// flatten turns the nested file into the dotted keys Fields names. A group whose value is not an
// object is reported under its own name so the warning points at what the user actually wrote.
func flatten(raw map[string]any) map[string]any {
	nested := map[string]bool{}
	for _, group := range groups {
		nested[group] = true
	}
	flat := make(map[string]any, len(raw))
	for key, value := range raw {
		members, isObject := value.(map[string]any)
		if !nested[key] || !isObject {
			flat[key] = value
			continue
		}
		for member, memberValue := range members {
			flat[key+"."+member] = memberValue
		}
	}
	return flat
}

// Save writes the settings that differ from the defaults, atomically, and returns the file path.
func Save(dir string, values Values) (string, error) {
	defaults := Defaults()
	stored := map[string]any{}
	for _, field := range Fields() {
		if Show(field.Key, values) == Show(field.Key, defaults) {
			continue
		}
		store(stored, field.Key, encode(field.Key, values))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("failed to create config directory %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode settings: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary config file in %s: %w", dir, err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = temp.Write(append(data, '\n')); err == nil {
		err = temp.Chmod(0o600)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("failed to write %s: %w", name, err)
	}
	if err := os.Rename(name, Path(dir)); err != nil {
		return "", fmt.Errorf("failed to install config file: %w", err)
	}
	return Path(dir), nil
}

// store places one dotted key into the nested shape config.json is written in.
func store(stored map[string]any, key string, value any) {
	group, member, nested := strings.Cut(key, ".")
	if !nested {
		stored[key] = value
		return
	}
	members, ok := stored[group].(map[string]any)
	if !ok {
		members = map[string]any{}
		stored[group] = members
	}
	members[member] = value
}

// encode is how a setting is written to disk, which is not always how it is held in memory:
// durations are stored as the text a human types.
func encode(key string, values Values) any {
	switch key {
	case "timeouts.review", "timeouts.fix":
		return Show(key, values)
	case "max_iterations":
		return values.MaxIterations
	case "retries":
		return values.Retries
	case "archive.keep":
		return values.Archive.Keep
	case "archive.raw_output":
		return values.Archive.RawOutput
	default:
		return Show(key, values)
	}
}

// Parse turns one setting typed in the settings pane into its stored representation.
func Parse(key, text string) (any, error) {
	text = strings.TrimSpace(text)
	switch key {
	case "reviewer.kind", "reviewer.name", "reset_command":
		return text, nil
	case "max_iterations":
		return wholeNumber(text, 1, 1000)
	case "retries":
		return wholeNumber(text, 0, 5)
	case "archive.keep":
		return wholeNumber(text, 1, 1000)
	case "archive.raw_output":
		value, err := strconv.ParseBool(text)
		if err != nil {
			return nil, fmt.Errorf("expected true or false")
		}
		return value, nil
	case "timeouts.review", "timeouts.fix":
		d, err := time.ParseDuration(text)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("expected a positive duration such as 30m")
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unknown setting")
	}
}

// Apply updates one parsed setting. Keeping this next to Parse ensures the pane and
// the file loader share one representation of every setting.
func Apply(values *Values, key string, value any) error {
	switch key {
	case "reviewer.kind":
		values.Reviewer.Kind = value.(string)
	case "reviewer.name":
		values.Reviewer.Name = value.(string)
	case "max_iterations":
		values.MaxIterations = value.(int)
	case "timeouts.review":
		values.Timeouts.Review = value.(time.Duration)
	case "timeouts.fix":
		values.Timeouts.Fix = value.(time.Duration)
	case "retries":
		values.Retries = value.(int)
	case "archive.keep":
		values.Archive.Keep = value.(int)
	case "archive.raw_output":
		values.Archive.RawOutput = value.(bool)
	case "reset_command":
		values.ResetCommand = value.(string)
	default:
		return fmt.Errorf("unknown setting")
	}
	return nil
}

// Show renders one setting for display, the inverse of Parse.
func Show(key string, values Values) string {
	switch key {
	case "reviewer.kind":
		return values.Reviewer.Kind
	case "reviewer.name":
		return values.Reviewer.Name
	case "max_iterations":
		return strconv.Itoa(values.MaxIterations)
	case "timeouts.review":
		return showDuration(values.Timeouts.Review)
	case "timeouts.fix":
		return showDuration(values.Timeouts.Fix)
	case "retries":
		return strconv.Itoa(values.Retries)
	case "archive.keep":
		return strconv.Itoa(values.Archive.Keep)
	case "archive.raw_output":
		return strconv.FormatBool(values.Archive.RawOutput)
	case "reset_command":
		return values.ResetCommand
	default:
		return ""
	}
}

func wholeNumber(text string, low, high int) (any, error) {
	n, err := strconv.Atoi(text)
	if err != nil {
		return nil, fmt.Errorf("expected a whole number")
	}
	if n < low || n > high {
		return nil, fmt.Errorf("must be a whole number from %d to %d", low, high)
	}
	return n, nil
}

func showDuration(value time.Duration) string {
	text := value.String()
	if strings.HasSuffix(text, "h0m0s") {
		return strings.TrimSuffix(text, "0m0s")
	}
	if strings.HasSuffix(text, "m0s") {
		return strings.TrimSuffix(text, "0s")
	}
	return text
}

// setJSON applies one decoded JSON value, rejecting the ones whose type cannot carry the setting.
func setJSON(values *Values, key string, value any) error {
	switch key {
	case "reviewer.kind", "reviewer.name", "reset_command":
		if value == nil {
			return Apply(values, key, "")
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be text")
		}
		return Apply(values, key, strings.TrimSpace(text))
	case "max_iterations", "retries", "archive.keep":
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) {
			return fmt.Errorf("must be a whole number")
		}
		parsed, err := Parse(key, strconv.Itoa(int(number)))
		if err != nil {
			return err
		}
		return Apply(values, key, parsed)
	case "archive.raw_output":
		flag, ok := value.(bool)
		if !ok {
			return fmt.Errorf("must be true or false")
		}
		return Apply(values, key, flag)
	case "timeouts.review", "timeouts.fix":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a duration string")
		}
		parsed, err := Parse(key, text)
		if err != nil {
			return err
		}
		return Apply(values, key, parsed)
	default:
		return fmt.Errorf("unknown setting")
	}
}
