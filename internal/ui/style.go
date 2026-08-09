package ui

func Bold(value string) string   { return "\x1b[1m" + value + "\x1b[0m" }
func Dim(value string) string    { return "\x1b[2m" + value + "\x1b[0m" }
func Red(value string) string    { return "\x1b[31m" + value + "\x1b[0m" }
func Invert(value string) string { return "\x1b[7m" + value + "\x1b[0m" }
