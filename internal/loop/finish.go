package loop

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// FinishResult is what a completed cleanup did, so the caller can report it
// without re-reading anything that has just been deleted.
type FinishResult struct {
	Run      RunRecord
	Stats    SummaryStats
	Removed  []string
	Archived bool
}

// Empty reports that the run left nothing behind to clean up.
func (r FinishResult) Empty() bool { return len(r.Removed) == 0 && !r.Archived }

// Digest is the closing report: what the run did, before the evidence for it is
// deleted from the working tree. Counts come from the decision record, so they
// answer the question a clean verdict alone cannot — whether the loop converged
// by fixing findings or by refusing them.
func (r FinishResult) Digest() []string {
	if r.Empty() {
		return []string{"nothing to finish"}
	}
	var lines []string
	if r.Run.ID != "" {
		header := "run " + r.Run.Started.Local().Format("2006-01-02 15:04")
		if r.Run.Author != "" {
			header += " · " + r.Run.Author + " ← " + r.Run.Reviewer
		}
		if r.Run.Rounds > 0 {
			header += fmt.Sprintf(" · %d round(s)", r.Run.Rounds)
		}
		if r.Run.Outcome != "" {
			header += " · " + r.Run.Outcome
		}
		lines = append(lines, header)
	}
	if r.Stats.Decisions() > 0 {
		lines = append(lines, fmt.Sprintf("decisions: %d applied · %d rejected · %d deferred", r.Stats.Applied, r.Stats.Rejected, r.Stats.Deferred))
	}
	if r.Stats.Tests != "" {
		lines = append(lines, "tests: "+r.Stats.Tests)
	}
	if r.Archived {
		lines = append(lines, "decisions archived to history/"+r.Run.ID+"/"+summaryArchive)
	}
	if len(r.Removed) > 0 {
		lines = append(lines, "removed "+strings.Join(r.Removed, ", "))
	}
	return lines
}

// Finish closes out a review: it archives the decision record, then deletes the
// two files the loop owns in the working tree. Nothing else in the repository is
// touched — the set is exactly what this plugin created, because guessing at
// which other files are temporary is how a cleanup deletes someone's work.
//
// Cleanup holds the run lock for its whole duration rather than sampling it,
// because a loop that starts between the check and the last delete would have
// its fresh files removed underneath it. Holding it also refuses to delete
// under a running loop, in either direction: a held lock with an unreadable
// record is a loop mid-startup, and removing the review file underneath it
// would break the freshness contract that decides whether a verdict belongs to
// the current round.
func Finish(stateDir, workspace, repository, reviewFile string) (FinishResult, error) {
	lock, err := AcquireLock(stateDir, workspace)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return FinishResult{}, fmt.Errorf("a review loop is running; stop it first")
		}
		return FinishResult{}, err
	}
	defer func() { _ = lock.Release() }()
	if reviewFile == SummaryFile {
		return FinishResult{}, fmt.Errorf("review_file must not be %s; it is reserved for review decisions", SummaryFile)
	}
	review, err := OpenReviewFile(repository, reviewFile)
	if err != nil {
		return FinishResult{}, err
	}
	defer func() { _ = review.Close() }()
	summary, err := OpenSummaryFile(repository)
	if err != nil {
		return FinishResult{}, err
	}
	defer func() { _ = summary.Close() }()

	log := Log{StateDir: stateDir}
	var result FinishResult
	contents, _, err := summary.Read()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return FinishResult{}, err
	}
	// The run is resolved whether or not there are decisions to archive: a review
	// that comes back clean on the first round never reaches an author phase, so
	// it has no summary, and reporting it as an anonymous deletion would drop the
	// only account of which run the files belonged to.
	run, tracked := LatestRun(stateDir, repository)
	// Archive before removing anything. The decision record is the only durable
	// account of what was applied and what was refused, and the next run deletes
	// it unread, so losing it here would make finishing a review destructive.
	if contents != "" {
		result.Stats = ParseSummary(contents)
		if !tracked {
			run = RunRecord{ID: NewRunID(time.Now()), Repository: repository, Started: time.Now().UTC(), Outcome: "finished outside a tracked run"}
			if err := log.WriteRun(run); err != nil {
				return FinishResult{}, err
			}
		}
		if err := log.ArchiveSummary(run.ID, contents); err != nil {
			return FinishResult{}, err
		}
		result.Archived = true
	}
	result.Run = run
	for _, file := range []*ReviewFile{review, summary} {
		exists, err := file.Exists()
		if err != nil {
			return FinishResult{}, err
		}
		if !exists {
			continue
		}
		if err := file.Remove(); err != nil {
			return FinishResult{}, err
		}
		result.Removed = append(result.Removed, file.Relative)
	}
	return result, nil
}
