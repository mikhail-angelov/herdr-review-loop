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

// Style decorates a rendered round. Markdown writes the markers `show` has always written; a pane
// writes the terminal's colors instead, and which colors those are is decided where the rest of
// the styling lives. One renderer walks the round either way: a findings pane that read
// differently from `show` would be a second document to keep in step with the first.
type Style struct {
	// Title decorates the document's own heading, Head a section's.
	Title func(text string) string
	Head  func(text string) string
	// Strong is what the eye should land on, Faint what it may skip.
	Strong func(text string) string
	Faint  func(text string) string
	// Severity and Action decorate a finding's weight and what the author did about it, which are
	// the two words a reader scans for.
	Severity func(severity string) string
	Action   func(action string) string
	// Event decorates one line of the run's event stream by the event that produced it, so the log
	// reads in the same colors the panel gives the same events.
	Event func(event, text string) string
}

// MarkdownStyle is the document `show` writes: markers, no color, the same bytes down a pipe as on
// a terminal.
func MarkdownStyle() Style {
	wrap := func(marker string) func(string) string {
		return func(text string) string { return marker + text + marker }
	}
	prefix := func(marker string) func(string) string {
		return func(text string) string { return marker + text }
	}
	return Style{
		Title: prefix("# "), Head: prefix("## "),
		Strong: wrap("**"), Faint: wrap("_"),
		Severity: plain, Action: wrap("**"),
		Event: func(_, text string) string { return text },
	}
}

func plain(text string) string { return text }

// fill replaces whatever a caller left unset with the text itself, so a half-given style renders a
// plain document rather than panicking on the first heading.
func (s Style) fill() Style {
	for _, field := range []*func(string) string{&s.Title, &s.Head, &s.Strong, &s.Faint, &s.Severity, &s.Action} {
		if *field == nil {
			*field = plain
		}
	}
	return s
}

// RenderMarkdown is the round as `show` reports it.
func RenderMarkdown(document RoundDocument) string { return Render(document, MarkdownStyle()) }

// Render is the only place a round becomes prose. Nothing is written to the working tree to show
// it: `show` prints the result and a pane pipes it to the user's pager.
func Render(document RoundDocument, style Style) string {
	style = style.fill()
	var out strings.Builder
	fmt.Fprintf(&out, "%s\n\n", style.Title(fmt.Sprintf("run %s · round %d", document.Run, document.Round)))
	review := document.Review
	switch review.Resolve() {
	case Clean:
		if len(review.Filtered) != 0 {
			fmt.Fprintf(&out, "%s — nothing cleared the verdict bar; %d finding(s) were filtered.\n", style.Strong("clean"), len(review.Filtered))
			break
		}
		fmt.Fprintf(&out, "%s — the reviewer had nothing left to raise.\n", style.Strong("clean"))
	case Blocked:
		fmt.Fprintf(&out, "%s — %d open question(s) for a human.\n", style.Strong("blocked"), len(review.OpenQuestions))
	case Dirty:
		fmt.Fprintf(&out, "%s\n", style.Strong(fmt.Sprintf("%d finding(s)", len(review.Findings))))
	case Contradictory:
		fmt.Fprintf(&out, "%s — the reviewer claimed findings and listed none.\n", style.Strong("unresolved"))
	}
	for _, finding := range review.Findings {
		renderFinding(&out, finding, document.Decisions, style)
	}
	renderQuestions(&out, review.OpenQuestions, style)
	renderFiltered(&out, review.Filtered, style)
	renderPreExisting(&out, review.PreExisting, style)
	renderTests(&out, document.Decisions, style)
	return out.String()
}

func renderFinding(out *strings.Builder, finding Finding, decisions Decisions, style Style) {
	// the severity is styled beside the heading rather than inside it: a color that ends mid-line
	// would end the heading's own styling with it
	fmt.Fprintf(out, "\n%s · %s · %s\n\n", style.Head(finding.ID), style.Severity(finding.Severity), finding.Location())
	fmt.Fprintf(out, "%s\n", style.Strong(finding.Title))
	labels := []string{finding.Verdict}
	if finding.Category != "" {
		labels = append(labels, finding.Category)
	}
	if finding.Regression {
		labels = append(labels, "regression")
	}
	fmt.Fprintf(out, "\n%s\n", style.Faint(strings.Join(labels, " · ")))
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
	fmt.Fprintf(out, "\nauthor: %s", style.Action(decision.Action))
	if decision.Note != "" {
		fmt.Fprintf(out, " — %s", decision.Note)
	}
	out.WriteString("\n")
}

func renderQuestions(out *strings.Builder, questions []OpenQuestion, style Style) {
	if len(questions) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n\n", style.Head("open questions"))
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
func renderFiltered(out *strings.Builder, findings []Finding, style Style) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n\nBelow the run's minimum verdict, and not sent to the author.\n\n", style.Head("filtered"))
	for _, finding := range findings {
		fmt.Fprintf(out, "- %s · %s · %s — %s\n", finding.Verdict, style.Severity(finding.Severity), finding.Location(), finding.Title)
	}
}

func renderPreExisting(out *strings.Builder, findings []Finding, style Style) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n\nReported as already true before the diff, and not sent to the author.\n\n", style.Head("pre-existing"))
	for _, finding := range findings {
		fmt.Fprintf(out, "- %s · %s — %s\n", style.Severity(finding.Severity), finding.Location(), finding.Title)
	}
}

func renderTests(out *strings.Builder, decisions Decisions, style Style) {
	if decisions.Tests.Outcome == "" && !decisions.Tests.Ran {
		return
	}
	fmt.Fprintf(out, "\n%s\n\n", style.Head("tests"))
	if !decisions.Tests.Ran {
		out.WriteString("the author reported not running them\n")
		return
	}
	fmt.Fprintf(out, "%s\n", decisions.Tests.Outcome)
}
