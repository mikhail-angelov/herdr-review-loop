package loop

import (
	"errors"
	"fmt"
)

// The exit codes a run ends with. "0 only on a clean review" is the whole point of the scheme; the
// rest exist so a spent budget, a broken setup, a question for a human, a cancellation and an
// agent that gave up are told apart from the code alone.
const (
	// ExitClean is a review that came back with nothing left to raise.
	ExitClean = 0
	// ExitFindings means findings remain and the round budget is spent. A normal outcome.
	ExitFindings = 1
	// ExitTool is the user's setup: configuration, no second agent, an archive that cannot be written.
	ExitTool = 2
	// ExitBlocked means a human is needed — an agent is blocked, or the reviewer asked a question.
	ExitBlocked = 3
	// ExitCanceled is a run stopped through `stop`.
	ExitCanceled = 4
	// ExitAgent is a stall, a crash or an unrecoverable parse failure that outlived the retry
	// budget. It is separate from ExitTool because the two ask for different things: a tool error
	// is fixed before rerunning, an agent failure is usually fixed by rerunning.
	ExitAgent = 5
)

// ExitError carries the code a run ended with, so the exit status is decided where the outcome is
// known rather than guessed at from an error string in main.
type ExitError struct {
	Code int
	Err  error
}

// Error reports the underlying failure; the code travels alongside it for the caller that exits.
func (e *ExitError) Error() string { return e.Err.Error() }

// Unwrap exposes the cause, so errors.Is and errors.As still reach it.
func (e *ExitError) Unwrap() error { return e.Err }

// Exit wraps an error with the code it should end the process with.
func Exit(code int, err error) error { return &ExitError{Code: code, Err: err} }

// Exitf wraps a newly formatted failure with its code.
func Exitf(code int, format string, args ...any) error {
	return &ExitError{Code: code, Err: fmt.Errorf(format, args...)}
}

// ExitCode reports the status a failure should exit with. An error that carries no code is a tool
// error: everything the loop itself decides is wrapped where it is decided.
func ExitCode(err error) int {
	if err == nil {
		return ExitClean
	}
	var coded *ExitError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ExitTool
}
