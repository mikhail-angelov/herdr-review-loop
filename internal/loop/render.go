package loop

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RoundDocument is one round as `show --format json` hands it over: the review the loop parsed and
// the decisions it completed, in one object so a consumer needs one read rather than two.
type RoundDocument struct {
	Run       string    `json:"run"`
	Round     int       `json:"round"`
	Review    Review    `json:"review"`
	Decisions Decisions `json:"decisions"`
}

// LoadRound reads one archived round. It reports false when the run kept no such round, which is
// the same answer for a rotated archive and for a round that never happened — both mean there is
// nothing left to render.
func LoadRound(stateDir, runID string, round int) (RoundDocument, bool) {
	review, found := ReadRoundReview(stateDir, runID, round)
	if !found {
		return RoundDocument{}, false
	}
	decisions, _ := ReadRoundDecisions(stateDir, runID, round)
	return RoundDocument{Run: runID, Round: round, Review: review, Decisions: decisions}, true
}

// RenderJSON is the machine-readable form of a round.
func RenderJSON(document RoundDocument) (string, error) {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode round %d: %w", document.Round, err)
	}
	return string(data) + "\n", nil
}

// RenderMarkdown is the only place a round becomes prose. `show` writes it to stdout and the
// history pane pipes it to the user's pager, so there is one renderer and no file in the working
// tree to drift out of sync with it.
func RenderMarkdown(document RoundDocument) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# run %s · round %d\n\n", document.Run, document.Round)
	review := document.Review
	switch review.Resolve() {
	case Clean:
		if len(review.Filtered) != 0 {
			fmt.Fprintf(&out, "**clean** — nothing cleared the verdict bar; %d finding(s) were filtered.\n", len(review.Filtered))
			break
		}
		out.WriteString("**clean** — the reviewer had nothing left to raise.\n")
	case Blocked:
		fmt.Fprintf(&out, "**blocked** — %d open question(s) for a human.\n", len(review.OpenQuestions))
	case Dirty:
		fmt.Fprintf(&out, "**%d finding(s)**\n", len(review.Findings))
	case Contradictory:
		out.WriteString("**unresolved** — the reviewer claimed findings and listed none.\n")
	}
	for _, finding := range review.Findings {
		renderFinding(&out, finding, document.Decisions)
	}
	renderQuestions(&out, review.OpenQuestions)
	renderFiltered(&out, review.Filtered)
	renderPreExisting(&out, review.PreExisting)
	renderTests(&out, document.Decisions)
	return out.String()
}

func renderFinding(out *strings.Builder, finding Finding, decisions Decisions) {
	fmt.Fprintf(out, "\n## %s · %s · %s\n\n", finding.ID, finding.Severity, finding.Location())
	fmt.Fprintf(out, "**%s**\n", finding.Title)
	labels := []string{finding.Verdict}
	if finding.Category != "" {
		labels = append(labels, finding.Category)
	}
	if finding.Regression {
		labels = append(labels, "regression")
	}
	fmt.Fprintf(out, "\n_%s_\n", strings.Join(labels, " · "))
	if finding.Body != "" {
		fmt.Fprintf(out, "\n%s\n", finding.Body)
	}
	if finding.Fix != "" {
		fmt.Fprintf(out, "\nfix: %s\n", finding.Fix)
	}
	decision, found := decisions.Find(finding.ID)
	if !found {
		return
	}
	fmt.Fprintf(out, "\nauthor: **%s**", decision.Action)
	if decision.Note != "" {
		fmt.Fprintf(out, " — %s", decision.Note)
	}
	out.WriteString("\n")
}

func renderQuestions(out *strings.Builder, questions []OpenQuestion) {
	if len(questions) == 0 {
		return
	}
	out.WriteString("\n## open questions\n\n")
	for _, question := range questions {
		out.WriteString("- " + question.Question)
		if question.File != "" {
			fmt.Fprintf(out, "  (%s)", Finding{File: question.File, Line: question.Line}.Location())
		}
		out.WriteString("\n")
	}
}

// renderFiltered keeps every withheld finding visible, so a bar set too high is legible in the run
// that suffered from it rather than only in the setting that caused it.
func renderFiltered(out *strings.Builder, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	out.WriteString("\n## filtered\n\nBelow the run's minimum verdict, and not sent to the author.\n\n")
	for _, finding := range findings {
		fmt.Fprintf(out, "- %s · %s · %s — %s\n", finding.Verdict, finding.Severity, finding.Location(), finding.Title)
	}
}

func renderPreExisting(out *strings.Builder, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	out.WriteString("\n## pre-existing\n\nReported as already true before the diff, and not sent to the author.\n\n")
	for _, finding := range findings {
		fmt.Fprintf(out, "- %s · %s — %s\n", finding.Severity, finding.Location(), finding.Title)
	}
}

func renderTests(out *strings.Builder, decisions Decisions) {
	if decisions.Tests.Outcome == "" && !decisions.Tests.Ran {
		return
	}
	out.WriteString("\n## tests\n\n")
	if !decisions.Tests.Ran {
		out.WriteString("the author reported not running them\n")
		return
	}
	fmt.Fprintf(out, "%s\n", decisions.Tests.Outcome)
}
