package ui

// The pane styles are raw escape sequences rather than a styling library: there are four of them,
// and every one resets afterwards so styled text can be concatenated freely.

// Bold renders emphasized text.
func Bold(value string) string { return "\x1b[1m" + value + "\x1b[0m" }

// Dim renders secondary text.
func Dim(value string) string { return "\x1b[2m" + value + "\x1b[0m" }

// Red renders failure text.
func Red(value string) string { return "\x1b[31m" + value + "\x1b[0m" }

// Invert renders a cursor block.
func Invert(value string) string { return "\x1b[7m" + value + "\x1b[0m" }
