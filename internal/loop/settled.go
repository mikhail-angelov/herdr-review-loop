package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// settledFile is where a repository's settled points outlive the run that settled them. It sits in
// the plugin's state directory rather than in the repository, because anything inside the
// repository is inside the diff under review.
const settledFile = "settled.json"

// SettledRecord is one repository's carried-forward journal. The repository is stored with it
// because a workspace's state directory is shared by every repository opened in it, and a settled
// point from one project means nothing in another.
type SettledRecord struct {
	Repository string    `json:"repository"`
	Entries    []Settled `json:"entries"`
}

func settledPath(stateDir string) string { return filepath.Join(stateDir, settledFile) }

// LoadSettled returns what earlier runs against this repository already argued out. It is what
// makes a stopped run cheap to restart: the reviewer is told what was rejected and why before it
// raises it again, so a second run resumes the conversation instead of starting it over.
func LoadSettled(stateDir, repository string) []Settled {
	data, err := os.ReadFile(settledPath(stateDir))
	if err != nil {
		return nil
	}
	var record SettledRecord
	if json.Unmarshal(data, &record) != nil || record.Repository != repository {
		return nil
	}
	return record.Entries
}

// SaveSettled publishes the journal as it stands. It is written after every round rather than at
// the end of a run, because the runs that most need their memory carried forward are the ones that
// do not reach an end.
func SaveSettled(stateDir, repository string, entries []Settled) error {
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	return writeJSON(settledPath(stateDir), SettledRecord{Repository: repository, Entries: entries})
}

// ClearSettled forgets what was settled. Finishing a review is the one moment that is right: the
// changes are being handed on, and the next review starts from the code rather than from the
// arguments the last one had about it.
func ClearSettled(stateDir string) { _ = os.Remove(settledPath(stateDir)) }
