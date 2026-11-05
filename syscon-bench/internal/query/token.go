package query

import (
	"strings"
	"unicode"
)

// Tokens splits message on non-alphanumeric characters and returns the runs
// whose rune length is at least 4. Case is preserved. The order is the order
// in the message.
func Tokens(message string) []string {
	return collectTokens(message, 0)
}

// collectTokens stops after limit kept tokens. limit <= 0 keeps every token.
// Each token is copied so the caller's message buffer can be released.
func collectTokens(message string, limit int) []string {
	var out []string
	start := -1
	runes := 0
	flush := func(end int) {
		if start < 0 {
			return
		}
		if runes >= 4 && (limit <= 0 || len(out) < limit) {
			out = append(out, strings.Clone(message[start:end]))
		}
		start = -1
		runes = 0
	}
	for i, r := range message {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
				runes = 0
			}
			runes++
			continue
		}
		flush(i)
		if limit > 0 && len(out) >= limit {
			return out
		}
	}
	flush(len(message))
	return out
}
