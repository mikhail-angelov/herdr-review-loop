package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Severity and verdict values. Both are optional in the reviewer's output and both have a default,
// so a reviewer that omits them still produces findings the loop can rank and filter.
const (
	SeverityHigh   = "high"
	SeverityMedium = "medium"
	SeverityLow    = "low"

	VerdictConfirmed = "confirmed"
	VerdictPlausible = "plausible"
)

// Status is the reviewer's own reading of its findings. It is advisory: Resolve decides what the
// round actually means by weighing it against the arrays.
const (
	StatusClean    = "clean"
	StatusFindings = "findings"
)

// Finding is one thing the reviewer wants changed. ID and Fingerprint are assigned by the loop,
// never by the model: models do not hold stable identifiers across turns, so they are not asked to.
type Finding struct {
	ID          string `json:"id,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	EndLine     int    `json:"end_line"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Verdict     string `json:"verdict"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	Fix         string `json:"fix"`
	Regression  bool   `json:"regression"`
}

// Location renders a finding's place for a human, with line 0 meaning the file as a whole.
func (f Finding) Location() string {
	if f.Line <= 0 {
		return f.File
	}
	if f.EndLine > f.Line {
		return fmt.Sprintf("%s:%d-%d", f.File, f.Line, f.EndLine)
	}
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}

// OpenQuestion is something the reviewer could not settle from the diff and that needs a human.
type OpenQuestion struct {
	Question string `json:"question"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// Review is one round's structured verdict, as the reviewer writes it and the loop reads it back.
// Filtered is the loop's own: findings the reviewer raised that did not clear min_verdict. They
// are kept here so nothing a run produced is lost, and they are never sent to the author.
type Review struct {
	Status        string         `json:"status"`
	Findings      []Finding      `json:"findings"`
	OpenQuestions []OpenQuestion `json:"open_questions"`
	PreExisting   []Finding      `json:"pre_existing"`
	Filtered      []Finding      `json:"filtered,omitempty"`
}

// Resolution is what a round's review means once the loop has resolved the reviewer's status
// against the arrays it came with. A clean verdict is only ever read from output the loop could
// parse, that agrees with itself, and that asks nothing.
type Resolution int

const (
	// Dirty means there are findings for the author to decide on.
	Dirty Resolution = iota
	// Clean means the reviewer had nothing left to raise and said so.
	Clean
	// Blocked means the reviewer asked a question only a human can answer.
	Blocked
	// Contradictory means the reviewer claimed findings and listed none, which in practice is a
	// truncated turn rather than clean code, so it must never produce a clean exit.
	Contradictory
)

// Resolve applies the three contradiction rules of the review contract, in the order that makes a
// question win over a verdict and an array win over the status line that describes it.
func (r Review) Resolve() Resolution {
	if len(r.OpenQuestions) > 0 {
		return Blocked
	}
	if len(r.Findings) > 0 {
		return Dirty
	}
	if strings.EqualFold(strings.TrimSpace(r.Status), StatusClean) {
		return Clean
	}
	return Contradictory
}

// AuthorView is the review as the author receives it: the findings it must decide on, and nothing
// it has no say over. Pre-existing and filtered findings are kept for the report, not sent on.
func (r Review) AuthorView() Review {
	return Review{Status: r.Status, Findings: r.Findings}
}

// Filter withholds the findings that did not clear the verdict bar. A false positive costs more
// in a loop than in a report — a human discards it, whereas the author goes and "fixes" something
// that is not there — so the bar is applied before the author sees anything.
//
// The round verdict is resolved after this, never before: a round whose findings were all
// filtered has nothing for the author to do, so it is clean. That it is clean by filtering rather
// than by agreement is what Filtered records, in the panel, the archive and the report alike.
func (r Review) Filter(minVerdict string) Review {
	if minVerdict != VerdictConfirmed || len(r.Findings) == 0 {
		return r
	}
	kept := make([]Finding, 0, len(r.Findings))
	var filtered []Finding
	for _, finding := range r.Findings {
		if finding.Verdict == VerdictPlausible {
			filtered = append(filtered, finding)
			continue
		}
		kept = append(kept, finding)
	}
	if len(filtered) == 0 {
		return r
	}
	r.Findings, r.Filtered = kept, filtered
	if len(kept) == 0 {
		r.Findings, r.Status = nil, StatusClean
	}
	return r
}

// IDs lists the ids the author is required to decide on, in the reviewer's own ranking order.
func (r Review) IDs() []string {
	ids := make([]string, 0, len(r.Findings))
	for _, finding := range r.Findings {
		ids = append(ids, finding.ID)
	}
	return ids
}

// Identify assigns this round's ids and fingerprints. Ids are unique within a run; fingerprints
// are how the same finding is recognized across the rounds of one run, where the reviewer is one
// agent in one session and a repeated title really is repeated nearly verbatim.
func (r *Review) Identify(round int) {
	for index := range r.Findings {
		r.Findings[index].ID = fmt.Sprintf("r%02d-%d", round, index+1)
		r.Findings[index].Fingerprint = Fingerprint(r.Findings[index])
	}
	for index := range r.PreExisting {
		r.PreExisting[index].ID = fmt.Sprintf("r%02d-p%d", round, index+1)
		r.PreExisting[index].Fingerprint = Fingerprint(r.PreExisting[index])
	}
}

// Fingerprint is the within-run identity of a finding. Exact-match hashing is enough here and only
// here: across runs and sessions a model does not repeat its wording, which is why there is no
// cross-run key.
func Fingerprint(finding Finding) string {
	sum := sha256.Sum256([]byte(finding.File + "\x00" + finding.Category + "\x00" + normalizeTitle(finding.Title)))
	return hex.EncodeToString(sum[:])[:12]
}

// normalizeTitle lowercases, collapses whitespace and strips punctuation.
func normalizeTitle(title string) string {
	var builder strings.Builder
	space := true
	for _, symbol := range strings.ToLower(title) {
		switch {
		case unicode.IsSpace(symbol):
			if !space {
				builder.WriteByte(' ')
				space = true
			}
		case unicode.IsPunct(symbol) || unicode.IsSymbol(symbol):
		default:
			builder.WriteRune(symbol)
			space = false
		}
	}
	return strings.TrimSpace(builder.String())
}

// ErrNoReview reports output the loop could find no review in at all, which is what sends a round
// into the degradation ladder.
var ErrNoReview = errors.New("no review could be read from the output")

// NotePrefixParseFallback marks the note a parse that had to fall back to an older form leaves
// behind, so the run can raise it as the event it is rather than only logging the text.
const NotePrefixParseFallback = "parse_fallback: "

// ParseReview reads the reviewer's output. It tries JSON first and the v1 markdown form second,
// and returns the notes describing everything it had to drop or fall back to, so the run's archive
// records a degradation rather than hiding it.
func ParseReview(raw string) (review Review, notes []string, err error) {
	if object, isJSON := lastJSONObject(stripFences(raw)); isJSON {
		if decodeErr := json.Unmarshal([]byte(object), &review); decodeErr == nil {
			review, notes = review.normalize()
			return review, notes, nil
		}
	}
	review, found := parseMarkdownReview(raw)
	if !found {
		return Review{}, nil, ErrNoReview
	}
	review, notes = review.normalize()
	return review, append([]string{NotePrefixParseFallback + "markdown"}, notes...), nil
}

// normalize applies every per-field rule of the contract, dropping what it cannot use and naming
// each drop. Order within the array is the reviewer's own ranking and is preserved.
func (r Review) normalize() (normalized Review, notes []string) {
	r.Findings, notes = normalizeFindings(r.Findings, notes)
	r.PreExisting, notes = normalizeFindings(r.PreExisting, notes)
	questions := make([]OpenQuestion, 0, len(r.OpenQuestions))
	for _, question := range r.OpenQuestions {
		if strings.TrimSpace(question.Question) == "" {
			notes = append(notes, "degraded: open-question")
			continue
		}
		questions = append(questions, question)
	}
	r.OpenQuestions = questions
	if len(r.OpenQuestions) == 0 {
		r.OpenQuestions = nil
	}
	return r, notes
}

func normalizeFindings(findings []Finding, notes []string) (kept []Finding, allNotes []string) {
	kept = make([]Finding, 0, len(findings))
	for _, finding := range findings {
		clean, reason := normalizeFinding(finding)
		if reason != "" {
			notes = append(notes, "degraded: "+reason)
			continue
		}
		kept = append(kept, clean)
	}
	if len(kept) == 0 {
		return nil, notes
	}
	return kept, notes
}

// normalizeFinding fills in the optional fields' defaults and returns the reason a finding cannot
// be used, empty when it can. A finding the loop cannot locate, or that says nothing, is one the
// author cannot act on.
func normalizeFinding(finding Finding) (normalized Finding, dropped string) {
	finding.File = strings.TrimSpace(strings.TrimPrefix(filepath.ToSlash(finding.File), "./"))
	if finding.File == "" || filepath.IsAbs(finding.File) {
		return Finding{}, "path"
	}
	clean := filepath.ToSlash(filepath.Clean(finding.File))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return Finding{}, "path"
	}
	finding.File = clean
	finding.Severity = strings.ToLower(strings.TrimSpace(finding.Severity))
	if finding.Severity != SeverityHigh && finding.Severity != SeverityLow {
		finding.Severity = SeverityMedium
	}
	finding.Verdict = strings.ToLower(strings.TrimSpace(finding.Verdict))
	if finding.Verdict != VerdictPlausible {
		finding.Verdict = VerdictConfirmed
	}
	if finding.Line < 0 {
		finding.Line = 0
	}
	if finding.EndLine < finding.Line {
		finding.EndLine = 0
	}
	finding.Title = strings.TrimSpace(finding.Title)
	if finding.Title == "" {
		finding.Title = strings.TrimSpace(finding.Body)
	}
	if finding.Title == "" {
		return Finding{}, "empty-finding"
	}
	return finding, ""
}

// stripFences removes markdown code fences, which models add around JSON whether or not they were
// asked to.
func stripFences(raw string) string {
	var kept []string
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// lastJSONObject returns the last balanced top-level object in the text. Last, not first, because
// models like to show an example of the schema and then give the real answer.
func lastJSONObject(text string) (string, bool) {
	var found string
	depth, start, inString, escaped := 0, -1, false, false
	for index, symbol := range text {
		if inString {
			switch {
			case escaped:
				escaped = false
			case symbol == '\\':
				escaped = true
			case symbol == '"':
				inString = false
			}
			continue
		}
		switch symbol {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = index
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				found = text[start : index+1]
			}
		}
	}
	return found, found != ""
}

// markdownFinding is the v1 review line: "- [high] path/to/file.ext:LINE — what — what to do".
// The em dash is what v1 asked for; a hyphen-minus separator is accepted too, because that is what
// a model substitutes when it reformats its own output.
var markdownFinding = regexp.MustCompile(`^[-*]\s*\[(high|medium|low)\]\s*([^\s:]+)(?::(\d+))?\s*(?:—|--|–)\s*(.*)$`)

// parseMarkdownReview reads the v1 prose form, which is step 2 of the degradation ladder. A
// reviewer that answers in last release's format costs the loop a note, not a round.
func parseMarkdownReview(raw string) (Review, bool) {
	review := Review{Status: StatusFindings}
	sawStatus := false
	for line := range strings.SplitSeq(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if upper := strings.ToUpper(trimmed); strings.HasPrefix(upper, "STATUS:") {
			sawStatus = true
			if strings.Contains(upper, "CLEAN") {
				review.Status = StatusClean
			}
			continue
		}
		match := markdownFinding.FindStringSubmatch(trimmed)
		if match == nil {
			continue
		}
		line, _ := strconv.Atoi(match[3])
		title, fix, _ := strings.Cut(match[4], "—")
		if fix == "" {
			title, fix, _ = strings.Cut(match[4], " -- ")
		}
		review.Findings = append(review.Findings, Finding{
			File:     match[2],
			Line:     line,
			Severity: match[1],
			Title:    strings.TrimSpace(title),
			Fix:      strings.TrimSpace(fix),
		})
	}
	if len(review.Findings) > 0 {
		review.Status = StatusFindings
	}
	return review, sawStatus || len(review.Findings) > 0
}
