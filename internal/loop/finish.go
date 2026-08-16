package loop

import (
	"errors"
	"fmt"
	"strings"
)

// FinishResult is what a completed cleanup did, so the caller can report it without re-reading
// anything that has just been deleted.
type FinishResult struct {
	Run      RunRecord
	Report   Report
	HadRun   bool
	Removed  bool
	Reported bool
}

// Empty reports that there was nothing left to finish.
func (r FinishResult) Empty() bool { return !r.Removed && !r.Reported }

// Digest is the closing report: what the run did, drawn from the archive rather than from anything
// in the working tree, so finishing is a report and not a last chance to read the evidence.
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
	if r.Reported {
		counts := map[string]int{}
		for _, finding := range r.Report.Findings {
			counts[finding.Action]++
		}
		lines = append(lines,
			fmt.Sprintf("findings: %d applied · %d rejected · %d deferred · %d missing · %d unreviewed",
				counts[ActionApplied], counts[ActionRejected], counts[ActionDeferred], counts[ActionMissing], counts[ActionUnreviewed]),
			"archived to history/"+r.Run.ID)
	}
	if r.Removed {
		lines = append(lines, "removed "+RunSubdir+"/")
	}
	return lines
}

// Finish closes out a review. The run archived itself as it went, so all that is left in the
// working tree is the run directory, and removing it leaves the working tree containing the code
// changes and nothing else. No file the user might have edited is touched.
//
// Cleanup holds the run lock for its whole duration rather than sampling it, because a loop that
// starts between the check and the delete would have its fresh files removed underneath it.
func Finish(stateDir, workspace, repository string) (FinishResult, error) {
	lock, err := AcquireLock(stateDir, workspace)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return FinishResult{}, fmt.Errorf("a review loop is running; stop it first")
		}
		return FinishResult{}, err
	}
	defer func() { _ = lock.Release() }()
	runDir, err := OpenRunDir(repository)
	if err != nil {
		return FinishResult{}, err
	}
	defer func() { _ = runDir.Close() }()
	var result FinishResult
	result.Run, result.HadRun = LatestRun(stateDir, repository)
	if result.HadRun {
		result.Report, result.Reported = ReadReport(stateDir, result.Run.ID)
	}
	present, err := runDir.Exists()
	if err != nil {
		return FinishResult{}, err
	}
	if present {
		if err := runDir.Remove(); err != nil {
			return FinishResult{}, err
		}
		result.Removed = true
	}
	return result, nil
}

// Summarize is the one-line form of a digest, for the log and the panel.
func (r FinishResult) Summarize() string { return strings.Join(r.Digest(), "; ") }
