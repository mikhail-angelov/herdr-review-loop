package loop

import (
	"fmt"
	"os"
	"path/filepath"
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

// Feed is what the panel renders: the newest run's event stream, its current phase, and the
// outcome of the last run that finished. It comes from the archive rather than from the journal,
// so stall and retry lines arrive as events instead of being recovered from prose.
type Feed struct {
	Phase   string
	Outcome string
	Lines   []string
}

// LatestFeed builds the panel's view of the newest recorded run. A state directory with no runs
// yet produces an empty feed rather than an error: there is nothing wrong, there is just nothing.
func LatestFeed(stateDir string, limit int) Feed {
	runs := ListRuns(stateDir)
	if len(runs) == 0 {
		return Feed{}
	}
	record := runs[0]
	feed := Feed{Outcome: record.Outcome}
	if feed.Outcome == "running" {
		feed.Outcome = ""
	}
	events := ReadEvents(ArchiveDir(stateDir, record.ID))
	for _, event := range events {
		if event.Event == EventPhaseStart {
			feed.Phase = fmt.Sprintf("round %d %s", event.Round, event.Phase)
		}
		if event.Event == EventPhaseDone {
			feed.Phase = ""
		}
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	for _, event := range events {
		feed.Lines = append(feed.Lines, FormatEvent(event))
	}
	return feed
}

// FormatEvent renders one event for the panel. The leading timestamp is what a narrow pane drops
// first, so it stays a fixed-width prefix.
func FormatEvent(event Event) string {
	line := fmt.Sprintf("[%s] round %d %s: %s", event.TS.Local().Format("15:04:05"), event.Round, event.Phase, event.Event)
	if event.Detail != "" {
		line += " — " + event.Detail
	}
	return line
}
