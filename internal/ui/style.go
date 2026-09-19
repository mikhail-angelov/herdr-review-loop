package ui

import "strings"

// The pane styles are raw escape sequences rather than a styling library: there are a handful of
// them, and every one resets afterwards so styled text can be concatenated freely.
//
// Styling is applied after clipping, never before: an escape sequence is not a column, and Clip
// counts runes.

// Bold renders emphasized text.
func Bold(value string) string { return "\x1b[1m" + value + "\x1b[0m" }

// Dim renders secondary text.
func Dim(value string) string { return "\x1b[2m" + value + "\x1b[0m" }

// Green renders a state that is settled and well.
func Green(value string) string { return "\x1b[32m" + value + "\x1b[0m" }

// Yellow renders work in progress, and the moments that are waiting for a person.
func Yellow(value string) string { return "\x1b[33m" + value + "\x1b[0m" }

// Cyan renders an agent that is busy: not a problem, not a resting state.
func Cyan(value string) string { return "\x1b[36m" + value + "\x1b[0m" }

// Red renders failure text.
func Red(value string) string { return "\x1b[31m" + value + "\x1b[0m" }

// Keys styles a hint line — "j/k move · enter edit · q close" — so the keys read as keys and not
// as the first word of a phrase. Every pane's footer goes through it, which is what keeps one
// keypress looking the same wherever it is offered.
func Keys(hints string) string {
	parts := strings.Split(hints, " · ")
	for index, hint := range parts {
		parts[index] = styleHint(hint)
	}
	return strings.Join(parts, Dim(" · "))
}

// styleHint emphasizes the key and dims what it does. A hint that is only a key is all key.
func styleHint(hint string) string {
	key, rest, split := strings.Cut(strings.TrimSpace(hint), " ")
	if !split {
		return Bold(hint)
	}
	return Bold(key) + " " + Dim(rest)
}

// Invert renders a cursor block.
func Invert(value string) string { return "\x1b[7m" + value + "\x1b[0m" }
