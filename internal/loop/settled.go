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

// SettledRecord is every repository's carried-forward journal, keyed by repository. One file holds
// them all because a workspace's state directory is shared by every repository opened in it, and a
// settled point from one project means nothing in another — but neither may evict the other.
type SettledRecord struct {
	Repositories map[string][]Settled `json:"repositories"`
}

func settledPath(stateDir string) string { return filepath.Join(stateDir, settledFile) }

// LoadSettled returns what earlier runs against this repository already argued out. It is what
// makes a stopped run cheap to restart: the reviewer is told what was rejected and why before it
// raises it again, so a second run resumes the conversation instead of starting it over.
func LoadSettled(stateDir, repository string) []Settled {
	return loadSettled(stateDir).Repositories[repository]
}

// loadSettled reads the journals of every repository. A file that cannot be read or understood is
// an empty record rather than an error: carrying nothing forward costs a round, losing a run does
// not.
func loadSettled(stateDir string) SettledRecord {
	data, err := os.ReadFile(settledPath(stateDir))
	if err != nil {
		return SettledRecord{Repositories: map[string][]Settled{}}
	}
	var record SettledRecord
	if json.Unmarshal(data, &record) != nil || record.Repositories == nil {
		return SettledRecord{Repositories: map[string][]Settled{}}
	}
	return record
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
	record := loadSettled(stateDir)
	record.Repositories[repository] = entries
	return writeJSON(settledPath(stateDir), record)
}

// ClearSettled forgets what this repository settled, and only this one. Finishing a review is the
// one moment that is right: the changes are being handed on, and the next review starts from the
// code rather than from the arguments the last one had about it. Every other repository's journal
// is left where it is, because finishing here says nothing about a review going on there.
func ClearSettled(stateDir, repository string) {
	record := loadSettled(stateDir)
	if _, ok := record.Repositories[repository]; !ok {
		return
	}
	delete(record.Repositories, repository)
	if len(record.Repositories) == 0 {
		_ = os.Remove(settledPath(stateDir))
		return
	}
	_ = writeJSON(settledPath(stateDir), record)
}
