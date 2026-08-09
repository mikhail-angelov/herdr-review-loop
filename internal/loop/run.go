package loop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
	"github.com/mikhail-angelov/herdr-review-loop/internal/herdr"
)

type Client interface {
	AgentClient
	AgentList(context.Context) ([]herdr.Agent, error)
	WorkspaceReportMetadata(context.Context, string, string, bool) error
	NotificationShow(context.Context, string, string) error
}
type Run struct {
	Client      Client
	Config      config.Values
	Environment herdr.Environment
	Log         Log
}

func (r Run) Execute(ctx context.Context, dryRun bool) error {
	agents, err := r.Client.AgentList(ctx)
	if err != nil {
		return err
	}
	author, ok := herdr.Find(agents, r.Environment.Context.FocusedPaneID)
	if !ok {
		return fmt.Errorf("run this from your agent's pane")
	}
	reviewer, err := herdr.PickReviewer(r.Config, agents, author, func(message string) { _ = r.Log.Write(message) })
	if err != nil {
		return err
	}
	if dryRun {
		_, err := fmt.Fprintf(os.Stdout, "author: %s\nreviewer: %s\n", herdr.Describe(author), herdr.Describe(reviewer))
		return err
	}
	if err := r.Log.Write(fmt.Sprintf("author %s; review by %s", herdr.Describe(author), herdr.Describe(reviewer))); err != nil {
		return err
	}
	repository, err := r.Environment.Repository()
	if err != nil {
		return err
	}
	review, err := OpenReviewFile(repository, r.Config.ReviewFile)
	if err != nil {
		return err
	}
	defer func() { _ = review.Close() }()
	lock, err := AcquireLock(r.Environment.StateDir, author.WorkspaceID)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	defer func() { _ = r.Client.WorkspaceReportMetadata(context.Background(), author.WorkspaceID, "", true) }()
	runID := time.Now().UTC().Format("2006-01-02T15-04-05-000000000Z07-00")
	for iteration := 1; iteration <= r.Config.MaxIterations; iteration++ {
		if err := r.Client.WorkspaceReportMetadata(ctx, author.WorkspaceID, fmt.Sprintf("review %d/%d", iteration, r.Config.MaxIterations), false); err != nil {
			_ = r.Log.Write("progress update failed: " + err.Error())
		}
		_ = r.Log.Write(fmt.Sprintf("--- iteration %d/%d: review", iteration, r.Config.MaxIterations))
		if err := r.Client.AgentFocus(ctx, herdr.Target(reviewer)); err != nil {
			return err
		}
		phase, cancel := context.WithTimeout(ctx, r.Config.ReviewTimeout)
		_, err = Settle(phase, r.Client, reviewer)
		if err == nil {
			err = ResetSession(phase, r.Client, reviewer, r.Config.ResetCommand, func(message string) { _ = r.Log.Write(message) })
		}
		if err == nil {
			_, err = Settle(phase, r.Client, reviewer)
		}
		if err == nil {
			err = review.EnsureParent()
		}
		if err == nil {
			err = review.Remove()
		}
		askedAt := time.Now()
		if err == nil {
			_, err = SubmitAndWait(phase, r.Client, reviewer, ReviewPrompt(r.Config.Scope, review.Absolute, iteration, r.Config.MaxIterations))
		}
		if err != nil {
			cancel()
			return r.abort(author.WorkspaceID, err)
		}
		contents, verdict, err := review.WaitForVerdict(phase.Done(), askedAt, 15*time.Second)
		cancel()
		if err != nil {
			return r.abort(author.WorkspaceID, err)
		}
		if err = r.Log.Archive(runID, iteration, contents); err != nil {
			return r.abort(author.WorkspaceID, err)
		}
		if verdict == Clean {
			_ = r.Client.AgentFocus(ctx, herdr.Target(author))
			_ = r.Log.Write(fmt.Sprintf("clean after %d iteration(s)", iteration))
			_ = r.Client.NotificationShow(ctx, "Review loop clean", fmt.Sprintf("clean after %d iteration(s)", iteration))
			return nil
		}
		_ = r.Log.Write(fmt.Sprintf("findings reported (%d lines)", len(splitLines(contents))))
		if iteration == r.Config.MaxIterations {
			break
		}
		if err := r.Client.WorkspaceReportMetadata(ctx, author.WorkspaceID, fmt.Sprintf("fix %d/%d", iteration, r.Config.MaxIterations), false); err != nil {
			_ = r.Log.Write("progress update failed: " + err.Error())
		}
		_ = r.Log.Write(fmt.Sprintf("--- iteration %d/%d: apply", iteration, r.Config.MaxIterations))
		if err := r.Client.AgentFocus(ctx, herdr.Target(author)); err != nil {
			return r.abort(author.WorkspaceID, err)
		}
		phase, cancel = context.WithTimeout(ctx, r.Config.FixTimeout)
		_, err = Settle(phase, r.Client, author)
		if err == nil {
			_, err = SubmitAndWait(phase, r.Client, author, FixPrompt(review.Absolute))
		}
		cancel()
		if err != nil {
			return r.abort(author.WorkspaceID, err)
		}
	}
	err = fmt.Errorf("stopped after max iterations with findings still open")
	_ = r.Log.Write(err.Error())
	_ = r.Client.NotificationShow(ctx, "Review loop stopped", err.Error())
	return err
}
func (r Run) abort(workspace string, err error) error {
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "cancelled"
	}
	_ = r.Log.Write(message)
	_ = r.Client.NotificationShow(context.Background(), "Review loop stopped", message)
	return err
}
func splitLines(contents string) []string {
	if contents == "" {
		return nil
	}
	return strings.Split(contents, "\n")
}
