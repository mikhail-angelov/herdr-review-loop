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

type Values struct {
	ReviewerKind  string
	ReviewerName  string
	MaxIterations int
	ReviewFile    string
	ReviewTimeout time.Duration
	FixTimeout    time.Duration
	ResetCommand  string
}

func Defaults() Values {
	return Values{MaxIterations: 10, ReviewFile: "review.md", ReviewTimeout: 30 * time.Minute, FixTimeout: 30 * time.Minute}
}

type Field struct {
	Key, Label, Hint string
	Kind             string
}

func Fields() []Field {
	return []Field{
		{"reviewer_kind", "reviewer kind", "agent kind that reviews; empty uses another kind", "optional"},
		{"reviewer_name", "reviewer name", "agent name or pane id; wins over reviewer kind", "optional"},
		{"max_iterations", "max iterations", "review/apply rounds before giving up", "count"},
		{"review_file", "review file", "reviewer output, relative to the repository", "path"},
		{"review_timeout", "review timeout", "budget for one review round", "duration"},
		{"fix_timeout", "fix timeout", "budget for applying a review", "duration"},
		{"reset_command", "reset command", "fallback reset command for unknown agent kinds", "optional"},
	}
}

func Path(dir string) string { return filepath.Join(dir, "config.json") }

// Load never lets a malformed user setting prevent commands such as stop from running.
func Load(dir string) (Values, []string) {
	values := Defaults()
	data, err := os.ReadFile(Path(dir))
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return values, []string{fmt.Sprintf("cannot read config.json: %v, using defaults", err)}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return values, []string{"config.json is not a JSON object of settings, using defaults"}
	}
	var warnings []string
	known := map[string]bool{}
	for _, field := range Fields() {
		known[field.Key] = true
	}
	for key := range raw {
		if !known[key] {
			warnings = append(warnings, key+" is not a herdr-review-loop setting, ignoring it")
		}
	}
	for key, encoded := range raw {
		var value any
		if err := json.Unmarshal(encoded, &value); err != nil {
			warnings = append(warnings, key+": invalid JSON, using default")
			continue
		}
		if err := setJSON(&values, key, value); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v, using default", key, err))
		}
	}
	return values, warnings
}

func Save(dir string, values Values) (string, error) {
	defaults := Defaults()
	stored := map[string]any{}
	if values.ReviewerKind != defaults.ReviewerKind {
		stored["reviewer_kind"] = values.ReviewerKind
	}
	if values.ReviewerName != defaults.ReviewerName {
		stored["reviewer_name"] = values.ReviewerName
	}
	if values.MaxIterations != defaults.MaxIterations {
		stored["max_iterations"] = values.MaxIterations
	}
	if values.ReviewFile != defaults.ReviewFile {
		stored["review_file"] = values.ReviewFile
	}
	if values.ReviewTimeout != defaults.ReviewTimeout {
		stored["review_timeout"] = values.ReviewTimeout.String()
	}
	if values.FixTimeout != defaults.FixTimeout {
		stored["fix_timeout"] = values.FixTimeout.String()
	}
	if values.ResetCommand != defaults.ResetCommand {
		stored["reset_command"] = values.ResetCommand
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return "", err
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
		return "", err
	}
	if err = os.Rename(name, Path(dir)); err != nil {
		return "", err
	}
	return Path(dir), nil
}

func Parse(key, text string) (any, error) {
	text = strings.TrimSpace(text)
	switch key {
	case "reviewer_kind", "reviewer_name", "reset_command":
		return text, nil
	case "review_file":
		return validatePath(text)
	case "max_iterations":
		n, err := strconv.Atoi(text)
		if err != nil {
			return nil, fmt.Errorf("expected a whole number")
		}
		if n < 1 || n > 1000 {
			return nil, fmt.Errorf("must be a whole number from 1 to 1000")
		}
		return n, nil
	case "review_timeout", "fix_timeout":
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
	case "reviewer_kind":
		values.ReviewerKind = value.(string)
	case "reviewer_name":
		values.ReviewerName = value.(string)
	case "max_iterations":
		values.MaxIterations = value.(int)
	case "review_file":
		values.ReviewFile = value.(string)
	case "review_timeout":
		values.ReviewTimeout = value.(time.Duration)
	case "fix_timeout":
		values.FixTimeout = value.(time.Duration)
	case "reset_command":
		values.ResetCommand = value.(string)
	default:
		return fmt.Errorf("unknown setting")
	}
	return nil
}

func Show(key string, values Values) string {
	switch key {
	case "reviewer_kind":
		return values.ReviewerKind
	case "reviewer_name":
		return values.ReviewerName
	case "max_iterations":
		return strconv.Itoa(values.MaxIterations)
	case "review_file":
		return values.ReviewFile
	case "review_timeout":
		return showDuration(values.ReviewTimeout)
	case "fix_timeout":
		return showDuration(values.FixTimeout)
	case "reset_command":
		return values.ResetCommand
	default:
		return ""
	}
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

func setJSON(values *Values, key string, value any) error {
	switch key {
	case "reviewer_kind", "reviewer_name", "reset_command":
		if value == nil {
			return setString(values, key, "")
		}
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be text")
		}
		return setString(values, key, strings.TrimSpace(text))
	case "review_file":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be text")
		}
		valid, err := validatePath(text)
		if err != nil {
			return err
		}
		values.ReviewFile = valid.(string)
		return nil
	case "max_iterations":
		number, ok := value.(float64)
		if !ok || number != float64(int(number)) || number < 1 || number > 1000 {
			return fmt.Errorf("must be a whole number from 1 to 1000")
		}
		values.MaxIterations = int(number)
		return nil
	case "review_timeout", "fix_timeout":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("must be a duration string")
		}
		parsed, err := Parse(key, text)
		if err != nil {
			return err
		}
		if key == "review_timeout" {
			values.ReviewTimeout = parsed.(time.Duration)
		} else {
			values.FixTimeout = parsed.(time.Duration)
		}
		return nil
	default:
		return nil
	}
}

func setString(values *Values, key, text string) error {
	switch key {
	case "reviewer_kind":
		values.ReviewerKind = text
	case "reviewer_name":
		values.ReviewerName = text
	case "reset_command":
		values.ResetCommand = text
	}
	return nil
}

func validatePath(text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("must not be empty")
	}
	clean := filepath.Clean(text)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.HasSuffix(text, string(filepath.Separator)) {
		return nil, fmt.Errorf("must name a file inside the repository")
	}
	return clean, nil
}
