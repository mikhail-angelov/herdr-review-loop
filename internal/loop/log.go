package loop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Log struct {
	StateDir string
	Output   *os.File
}

func (l Log) Path() string { return filepath.Join(l.StateDir, "herdr-review-loop.log") }
func (l Log) Write(message string) error {
	if err := os.MkdirAll(l.StateDir, 0o755); err != nil {
		return err
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), message)
	file, err := os.OpenFile(l.Path(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(line)
	closeErr := file.Close()
	if l.Output != nil {
		_, _ = l.Output.WriteString(line)
	}
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
func (l Log) Tail() (string, error) {
	data, err := os.ReadFile(l.Path())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if len(data) > 8192 {
		data = data[len(data)-8192:]
	}
	return string(data), nil
}
func (l Log) Archive(runID string, iteration int, contents string) error {
	dir := filepath.Join(l.StateDir, "history", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("iteration-%02d.md", iteration)), []byte(contents), 0o644)
}
func Phase(log string) string {
	var phase string
	for _, line := range strings.Split(log, "\n") {
		if index := strings.Index(line, "--- iteration "); index >= 0 {
			if strings.Contains(line[index:], ": review") {
				phase = "reviewing"
			}
			if strings.Contains(line[index:], ": apply") {
				phase = "applying"
			}
		}
	}
	return phase
}
func LastOutcome(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := lines[index]
		if strings.Contains(line, "clean after") || strings.Contains(line, "stopped after") || strings.Contains(line, "cancelled") {
			return line
		}
	}
	return ""
}
