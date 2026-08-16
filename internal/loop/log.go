package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Log is the loop's append-only journal. It is the only channel the panel and the CLI share, so
// every line is written to the file and, when Output is set, echoed to the terminal.
type Log struct {
	StateDir string
	Output   *os.File
}

// Path is the journal file inside the plugin's state directory.
func (l Log) Path() string { return filepath.Join(l.StateDir, "herdr-review-loop.log") }

// Write appends one timestamped line.
func (l Log) Write(message string) error {
	if err := os.MkdirAll(l.StateDir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", l.StateDir, err)
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), message)
	file, err := os.OpenFile(l.Path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", l.Path(), err)
	}
	_, writeErr := file.WriteString(line)
	closeErr := file.Close()
	if l.Output != nil {
		_, _ = l.Output.WriteString(line)
	}
	if writeErr != nil {
		return fmt.Errorf("failed to write %s: %w", l.Path(), writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close %s: %w", l.Path(), closeErr)
	}
	return nil
}

// Tail returns the last 8KB of the journal, which is what the panel renders.
func (l Log) Tail() (string, error) {
	data, err := os.ReadFile(l.Path())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", l.Path(), err)
	}
	if len(data) > 8192 {
		data = data[len(data)-8192:]
	}
	return string(data), nil
}

// Archive keeps one round's findings under the run's history directory, so a finished run can be
// re-read after the journal has rolled past it.
func (l Log) Archive(runID string, iteration int, contents string) error {
	dir := filepath.Join(l.StateDir, "history", runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("failed to create history directory %s: %w", dir, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("iteration-%02d.md", iteration))
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// Phase reads the current round and activity out of the journal tail, for the panel's status line.
func Phase(log string) string {
	var phase string
	for _, line := range strings.Split(log, "\n") {
		if index := strings.Index(line, "--- iteration "); index >= 0 {
			iteration := strings.TrimPrefix(strings.SplitN(line[index:], ":", 2)[0], "--- iteration ")
			if strings.Contains(line[index:], ": review") {
				phase = iteration + " reviewing"
			}
			if strings.Contains(line[index:], ": apply") {
				phase = iteration + " applying"
			}
		}
	}
	return phase
}

// LastOutcome finds the line describing how the most recent run ended.
func LastOutcome(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := lines[index]
		if strings.Contains(line, "clean after") || strings.Contains(line, "stopped after") || strings.Contains(line, "canceled") {
			return line
		}
	}
	return ""
}
