// Package config loads and validates the review-loop configuration.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reviewer selects the second agent: by name when one is given, otherwise by kind.
type Reviewer struct{ Kind, Name string }

// Timeouts are the per-phase budgets a round is allowed to spend. Stall is independent of the
// phase budget: an agent that goes silent is not one that is still working slowly.
type Timeouts struct{ Review, Fix, Stall time.Duration }

// Archive bounds what finished runs leave behind in the plugin state directory.
type Archive struct {
	Keep      int
	RawOutput bool
}

// Round is one entry of the round policy: how hard the reviewer should look this round, which of
// its commands to use, and what else to tell it. The last entry repeats for every further round.
type Round struct {
	Level           string `json:"level,omitempty"`
	Command         string `json:"command,omitempty"`
	Instructions    string `json:"instructions,omitempty"`
	RegressionsOnly bool   `json:"regressions_only,omitempty"`
}

// Values is the effective configuration: the layers merged over the built-in defaults. Rounds and
// ReviewCommand are the only non-scalar settings; Same compares two of these, because a slice and
// a map put the struct out of reach of ==.
type Values struct {
	Profile       string
	Scope         string
	Reviewer      Reviewer
	MaxIterations int
	MinVerdict    string
	Timeouts      Timeouts
	Retries       int
	Archive       Archive
	ReviewCommand map[string]string
	ResetCommand  string
	Rounds        []Round
}

// Defaults are the built-in settings, decoded from the embedded defaults layer. Every other layer
// is merged over a copy of these, so the compiled-in file is the one place a default is written.
func Defaults() Values { return builtinDefaults() }

// builtinDefaults decodes the embedded defaults once. A failure here is a broken build rather than
// a broken installation — the file is compiled in and TestBuiltinDefaultsDecodeCleanly covers it —
// so it is reported as the programming error it is instead of degrading a run that cannot be right.
var builtinDefaults = sync.OnceValue(func() Values {
	values := Values{}
	compiled, found := readDocument(builtin, defaultsRoot+"/config.json", LayerDefault, "built-in defaults")
	if !found {
		panic(fmt.Sprintf("built-in defaults are invalid: %v", compiled.warnings))
	}
	if warnings := compiled.applyTo(&values, nil); len(warnings) != 0 {
		panic(fmt.Sprintf("built-in defaults are invalid: %v", warnings))
	}
	return values
})

// Same reports whether two configurations agree on every setting the settings pane can edit. The
// pane needs this rather than == because Values carries a slice and a map it never touches.
func Same(a, b Values) bool {
	for _, field := range Fields() {
		if Show(field.Key, a) != Show(field.Key, b) {
			return false
		}
	}
	return true
}

// Field describes one setting for the settings pane: its key, how to label it and how to read it.
type Field struct {
	Key, Label, Hint string
	Kind             string
}

// Fields lists every scalar setting, in the order the settings pane shows them. A key containing a
// dot names a member of the nested object of the same name in config.json. The structured settings
// — rounds and review_command — are absent on purpose: they are edited in a file, not a pane.
func Fields() []Field {
	return []Field{
		{"profile", "profile", "round policy to run; a file under profiles/", "optional"},
		{"scope", "scope", "worktree, or text:<path> to review one document", "optional"},
		{"reviewer.kind", "reviewer kind", "agent kind that reviews; empty uses another kind", "optional"},
		{"reviewer.name", "reviewer name", "agent name or pane id; wins over reviewer kind", "optional"},
		{"max_iterations", "max iterations", "review/apply rounds before giving up", "count"},
		{"min_verdict", "min verdict", "confirmed keeps only what the reviewer stands behind", "choice"},
		{"timeouts.review", "review timeout", "budget for one review round", "duration"},
		{"timeouts.fix", "fix timeout", "budget for applying a review", "duration"},
		{"timeouts.stall", "stall timeout", "silence from a pane that counts as a stalled agent", "duration"},
		{"retries", "retries", "repeat attempts per phase after the first, 0 to 5", "count"},
		{"archive.keep", "archive keep", "how many finished runs keep their archive", "count"},
		{"archive.raw_output", "archive raw output", "keep verbatim agent output in the archive", "bool"},
		{"reset_command", "reset command", "fallback reset command for unknown agent kinds", "optional"},
	}
}

// groups are the nested objects config.json carries, so a dotted field key and a JSON object stay
// two views of one setting rather than two settings. review_command is not among them: its members
// are agent kinds the loop does not know in advance, so it is carried whole.
var groups = []string{"reviewer", "timeouts", "archive"}

// structured are the settings that are not scalars and so have no pane field, plus the one key a
// profile carries for its reader. They are known keys all the same, and never warned about.
var structured = []string{"rounds", "review_command", "description"}

// Path is the config file inside a layer's directory.
func Path(dir string) string { return filepath.Join(dir, "config.json") }

// Save writes the settings that differ from the defaults, atomically, and returns the file path.
// It merges into whatever the file already holds rather than replacing it, because the pane edits
// only the scalar settings and must not drop a hand-written rounds or review_command.
func Save(dir string, values Values) (string, error) {
	stored := map[string]any{}
	if data, err := os.ReadFile(Path(dir)); err == nil {
		var existing map[string]any
		if json.Unmarshal(data, &existing) == nil && existing != nil {
			stored = existing
		}
	}
	overlay(stored, values)
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

// Example documents every setting, its default and what it does. It is generated from Fields so
// it cannot drift from the loader, and it is written beside config.json because the format is
// strict JSON and cannot carry comments.
func Example() string {
	defaults := Defaults()
	var page strings.Builder
	page.WriteString("# .review-loop/config.json\n\n")
	page.WriteString("Every key is optional. What you leave out comes from the layer below: this file, then\n")
	page.WriteString("your own config.json, then the selected profile, then the built-in defaults. A key that\n")
	page.WriteString("cannot be read is a warning and the default, never a failed run.\n\n")
	page.WriteString("Keys with a dot are members of the nested object of the same name:\n")
	page.WriteString("`\"timeouts\": {\"stall\": \"5m\"}` is the key `timeouts.stall`.\n\n")
	page.WriteString("| Key | Default | What it does |\n|---|---|---|\n")
	for _, field := range Fields() {
		shown := Show(field.Key, defaults)
		if shown == "" {
			shown = "_unset_"
		}
		fmt.Fprintf(&page, "| `%s` | `%s` | %s |\n", field.Key, shown, field.Hint)
	}
	page.WriteString("| `rounds` | from the profile | the round policy: one object per round, `{\"level\", \"command\", \"instructions\", \"regressions_only\"}`. The last entry repeats for every further round, and the array is replaced whole by the layer that defines it |\n")
	page.WriteString("| `review_command` | `{}` | per-agent-kind override of the review command, keyed by kind |\n")
	page.WriteString("\nA profile is a file of these same keys under a name, in `profiles/<name>.json`. Dropping one\n")
	page.WriteString("in place is the entire registration procedure; `review --profile <name>` selects it.\n")
	return page.String()
}

// Render is the config.json text for the settings that differ from the defaults, and nothing
// else. `init` writes it into a new project: a file holding the resolved state would silently
// freeze today's defaults, and a project inherits every default it has no opinion about.
func Render(values Values) string {
	stored := map[string]any{}
	overlay(stored, values)
	overlayStructured(stored, values)
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return "{}\n"
	}
	return string(data) + "\n"
}

// overlay writes the settings that differ from the defaults into the nested shape config.json is
// written in, and clears the ones that no longer do. Everything it does not name is left alone,
// which is what keeps a hand-written rounds or review_command through a save from the pane.
func overlay(stored map[string]any, values Values) {
	defaults := Defaults()
	for _, field := range Fields() {
		if Show(field.Key, values) == Show(field.Key, defaults) {
			remove(stored, field.Key)
			continue
		}
		store(stored, field.Key, encode(field.Key, values))
	}
}

// overlayStructured writes the two non-scalar settings when they differ from the defaults. It
// belongs to Render and not to overlay: a save from the settings pane must leave a hand-written
// rounds or review_command exactly as it stands, while a file init generates has to carry them, or
// the project reproduces a review policy its author never chose.
func overlayStructured(stored map[string]any, values Values) {
	defaults := Defaults()
	if !slices.Equal(values.Rounds, defaults.Rounds) {
		stored["rounds"] = values.Rounds
	}
	if !maps.Equal(values.ReviewCommand, defaults.ReviewCommand) {
		stored["review_command"] = values.ReviewCommand
	}
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

// remove drops one dotted key, and the group it lived in once that group is empty, so a setting
// restored to its default leaves no trace in the file.
func remove(stored map[string]any, key string) {
	group, member, nested := strings.Cut(key, ".")
	if !nested {
		delete(stored, key)
		return
	}
	members, ok := stored[group].(map[string]any)
	if !ok {
		return
	}
	delete(members, member)
	if len(members) == 0 {
		delete(stored, group)
	}
}

// encode is how a setting is written to disk, which is not always how it is held in memory:
// durations are stored as the text a human types.
func encode(key string, values Values) any {
	switch key {
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
	case "profile":
		if strings.ContainsAny(text, `/\`) || text == "." || text == ".." {
			return nil, fmt.Errorf("expected a profile name, not a path")
		}
		return text, nil
	case "scope":
		return parseScope(text)
	case "min_verdict":
		if text != VerdictConfirmed && text != VerdictPlausible {
			return nil, fmt.Errorf("expected %s or %s", VerdictConfirmed, VerdictPlausible)
		}
		return text, nil
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
	case "timeouts.review", "timeouts.fix", "timeouts.stall":
		d, err := time.ParseDuration(text)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("expected a positive duration such as 30m")
		}
		return d, nil
	default:
		return nil, fmt.Errorf("unknown setting")
	}
}

// The two verdicts a reviewer labels a finding with, and the bar min_verdict sets against them.
const (
	VerdictConfirmed = "confirmed"
	VerdictPlausible = "plausible"
)

// ScopeWorktree reviews the uncommitted changes; ScopeTextPrefix names a document to review whole.
const (
	ScopeWorktree   = "worktree"
	ScopeTextPrefix = "text:"
)

func parseScope(text string) (any, error) {
	if text == ScopeWorktree {
		return text, nil
	}
	if path, ok := strings.CutPrefix(text, ScopeTextPrefix); ok && strings.TrimSpace(path) != "" {
		return text, nil
	}
	return nil, fmt.Errorf("expected %s or %s<path>", ScopeWorktree, ScopeTextPrefix)
}

// Apply updates one parsed setting. Keeping this next to Parse ensures the pane and
// the file loader share one representation of every setting.
func Apply(values *Values, key string, value any) error {
	switch key {
	case "profile":
		values.Profile = value.(string)
	case "scope":
		values.Scope = value.(string)
	case "reviewer.kind":
		values.Reviewer.Kind = value.(string)
	case "reviewer.name":
		values.Reviewer.Name = value.(string)
	case "max_iterations":
		values.MaxIterations = value.(int)
	case "min_verdict":
		values.MinVerdict = value.(string)
	case "timeouts.review":
		values.Timeouts.Review = value.(time.Duration)
	case "timeouts.fix":
		values.Timeouts.Fix = value.(time.Duration)
	case "timeouts.stall":
		values.Timeouts.Stall = value.(time.Duration)
	case "retries":
		values.Retries = value.(int)
	case "archive.keep":
		values.Archive.Keep = value.(int)
	case "archive.raw_output":
		values.Archive.RawOutput = value.(bool)
	case "reset_command":
		values.ResetCommand = value.(string)
	case "rounds":
		values.Rounds = value.([]Round)
	case "review_command":
		values.ReviewCommand = value.(map[string]string)
	default:
		return fmt.Errorf("unknown setting")
	}
	return nil
}

// Show renders one setting for display, the inverse of Parse.
func Show(key string, values Values) string {
	switch key {
	case "profile":
		return values.Profile
	case "scope":
		return values.Scope
	case "reviewer.kind":
		return values.Reviewer.Kind
	case "reviewer.name":
		return values.Reviewer.Name
	case "max_iterations":
		return strconv.Itoa(values.MaxIterations)
	case "min_verdict":
		return values.MinVerdict
	case "timeouts.review":
		return showDuration(values.Timeouts.Review)
	case "timeouts.fix":
		return showDuration(values.Timeouts.Fix)
	case "timeouts.stall":
		return showDuration(values.Timeouts.Stall)
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
	case "profile", "scope", "min_verdict", "reviewer.kind", "reviewer.name", "reset_command":
		if value == nil {
			return Apply(values, key, "")
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be text")
		}
		parsed, err := Parse(key, text)
		if err != nil {
			return err
		}
		return Apply(values, key, parsed)
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
	case "timeouts.review", "timeouts.fix", "timeouts.stall":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a duration string")
		}
		parsed, err := Parse(key, text)
		if err != nil {
			return err
		}
		return Apply(values, key, parsed)
	case "rounds":
		rounds, err := decodeRounds(value)
		if err != nil {
			return err
		}
		return Apply(values, key, rounds)
	case "review_command":
		commands, err := decodeReviewCommands(value)
		if err != nil {
			return err
		}
		return Apply(values, key, commands)
	default:
		return fmt.Errorf("unknown setting")
	}
}

// decodeRounds reads the round policy. The array is taken whole or not at all: a half-merged
// policy is behavior nobody can debug, and one bad entry says nothing about the ones beside it.
func decodeRounds(value any) ([]Round, error) {
	entries, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of rounds")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("must name at least one round")
	}
	rounds := make([]Round, 0, len(entries))
	for index, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("round %d is not an object", index+1)
		}
		var round Round
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&round); err != nil {
			return nil, fmt.Errorf("round %d: %w", index+1, err)
		}
		rounds = append(rounds, round)
	}
	return rounds, nil
}

// decodeReviewCommands reads the per-kind override of the adapter table. Any kind is accepted,
// known or not: an entry for an unknown kind is what makes that kind supported without a release.
func decodeReviewCommands(value any) (map[string]string, error) {
	members, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be an object keyed by agent kind")
	}
	commands := make(map[string]string, len(members))
	for kind, command := range members {
		text, ok := command.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be text", kind)
		}
		commands[kind] = strings.TrimSpace(text)
	}
	return commands, nil
}
