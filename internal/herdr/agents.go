package herdr

import (
	"fmt"
	"strings"

	"github.com/mikhail-angelov/herdr-review-loop/internal/config"
)

// Agent is one coding agent running in a Herdr pane.
type Agent struct {
	PaneID         string `json:"pane_id"`
	WorkspaceID    string `json:"workspace_id"`
	Name           string `json:"name"`
	Kind           string `json:"agent"`
	Status         string `json:"agent_status"`
	StateChangeSeq int64  `json:"state_change_seq"`
}

// Find returns the agent occupying the given pane.
func Find(agents []Agent, paneID string) (Agent, bool) {
	for _, agent := range agents {
		if agent.PaneID == paneID {
			return agent, true
		}
	}
	return Agent{}, false
}

// Target is how an agent is addressed on the Herdr command line: its name when it has one, since
// names survive a pane being recreated, and the pane id otherwise.
func Target(agent Agent) string {
	if agent.Name != "" {
		return agent.Name
	}
	return agent.PaneID
}

// Describe renders an agent for humans, in log lines and the panel.
func Describe(agent Agent) string {
	suffix := ""
	if agent.Name != "" {
		suffix = " (" + agent.Name + ")"
	}
	return fmt.Sprintf("%s @ %s%s", fallback(agent.Kind, "?"), agent.PaneID, suffix)
}

// PickReviewer chooses who reviews the author's work: the configured agent by name, or otherwise
// any agent of a different kind in the same workspace, so the review comes from a second opinion
// rather than from the same model. note, when set, reports an ambiguous choice.
func PickReviewer(cfg config.Values, agents []Agent, author Agent, note func(string)) (Agent, error) {
	var candidates []Agent
	for _, agent := range agents {
		if agent.WorkspaceID == author.WorkspaceID && agent.PaneID != author.PaneID {
			candidates = append(candidates, agent)
		}
	}
	if cfg.ReviewerName != "" {
		for _, agent := range candidates {
			if agent.Name == cfg.ReviewerName || agent.PaneID == cfg.ReviewerName {
				return agent, nil
			}
		}
		return Agent{}, fmt.Errorf("no agent %q in workspace %s", cfg.ReviewerName, author.WorkspaceID)
	}
	var matches []Agent
	for _, agent := range candidates {
		if cfg.ReviewerKind != "" && agent.Kind == cfg.ReviewerKind || cfg.ReviewerKind == "" && agent.Kind != "" && agent.Kind != author.Kind {
			matches = append(matches, agent)
		}
	}
	if len(matches) == 0 {
		descriptions := make([]string, 0, len(candidates))
		for _, agent := range candidates {
			descriptions = append(descriptions, Describe(agent))
		}
		found := strings.Join(descriptions, ", ")
		if found == "" {
			found = "no other agent at all"
		}
		return Agent{}, fmt.Errorf("need a reviewer in workspace %s to review %s — found %s", author.WorkspaceID, Describe(author), found)
	}
	if len(matches) > 1 && note != nil {
		note(fmt.Sprintf("%d candidate reviewers, using the first", len(matches)))
	}
	return matches[0], nil
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}
