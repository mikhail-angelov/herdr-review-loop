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

// Feed is what the panel renders: the newest run's event stream, its current phase, and the
// outcome of the last run that finished. It comes from the archive rather than from the journal,
// so stall and retry lines arrive as events instead of being recovered from prose.
type Feed struct {
	Phase   string
	Outcome string
	Lines   []FeedLine
}

// FeedLine is one event as the panel prints it, with the event name kept beside the text. The
// panel decides how much a stall should stand out; the loop only says that it was one.
type FeedLine struct {
	Text  string
	Event string
}

// LatestFeed builds the panel's view of the newest run recorded for one repository. The state
// directory is shared by every workspace the plugin runs in, so the repository is what scopes a
// panel to its own work: a space that has never been reviewed shows nothing, rather than showing
// the last thing that happened somewhere else. No runs at all is that same nothing, not an error.
func LatestFeed(stateDir, repository string, limit int) Feed {
	record, found := LatestRun(stateDir, repository)
	if !found {
		return Feed{}
	}
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
		feed.Lines = append(feed.Lines, FeedLine{Text: FormatEvent(event), Event: event.Event})
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

// LogDocument is the panel's feed at full width: the newest run for a repository, every event it
// recorded with nothing clipped, and the journal underneath it. The panel has one line per event
// and a pane thirty columns wide, so anything with a detail worth reading is cut off there; this is
// the same material for a pager, where it can be scrolled and searched. It takes the same Style the
// findings document does, because the two are read in the same kind of pane and an event that is
// red in the panel has no business being plain here.
func LogDocument(stateDir, repository string, style Style) string {
	record, found := LatestRun(stateDir, repository)
	if !found {
		return "no recorded runs for this repository yet.\n"
	}
	var page strings.Builder
	fmt.Fprintf(&page, "%s\n", style.Title(fmt.Sprintf("run %s · %s", record.Started.Local().Format("2006-01-02 15:04"), record.Repository)))
	if record.Author != "" {
		fmt.Fprintf(&page, "%s\n", style.Faint(fmt.Sprintf("%s ← %s", record.Author, record.Reviewer)))
	}
	fmt.Fprintf(&page, "%d round(s) · %s\n\n%s\n", record.Rounds, style.Strong(record.Outcome), style.Head("events"))
	events := ReadEvents(ArchiveDir(stateDir, record.ID))
	if len(events) == 0 {
		page.WriteString("  none recorded yet\n")
	}
	for _, event := range events {
		page.WriteString("  " + style.Event(event.Event, FormatEvent(event)) + "\n")
	}
	// the journal is one file for the whole state directory, so it is labeled rather than filtered:
	// the lines that are not this run's are still the ones that explain a run that never started
	tail, err := (Log{StateDir: stateDir}).Tail()
	if err != nil || strings.TrimSpace(tail) == "" {
		return page.String()
	}
	fmt.Fprintf(&page, "\n%s\n", style.Head("journal — every workspace, newest last"))
	for _, line := range strings.Split(strings.TrimRight(tail, "\n"), "\n") {
		page.WriteString("  " + style.Faint(line) + "\n")
	}
	return page.String()
}
