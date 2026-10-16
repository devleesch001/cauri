package store

import (
	"net/netip"
	"strings"
)

// How a filter typed by hand matches a text column:
//
//	test       starts with test (the default)
//	*test*     contains test; * and % are the same wildcard, anywhere
//	"test"     exactly test, case included
//
// A complete IP address matches exactly; an incomplete one is a start,
// taken literally ("1.2.3" also finds 1.2.30.x). Wildcard and start
// matches ignore case (of ASCII letters only under SQLite, whose LOWER
// knows no other); _ and \ are themselves. The values of lists and links
// are no such filters: links quote theirs.

// matchMode is how a value without quotes nor wildcard matches.
type matchMode int

const (
	matchStart matchMode = iota // starts with it
	matchExact                  // exactly, as purges want: never wider than written
)

// match returns the condition of a filter value on col (an SQL column
// expression of the caller, never input) and its argument, for Where.
func match(col, value string, mode matchMode) (string, any) {
	if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
		return col + " = ?", value[1 : len(value)-1]
	}
	if !strings.ContainsAny(value, "*%") {
		if mode == matchExact {
			return col + " = ?", value
		}
		value += "*"
	}
	return "LOWER(" + col + `) LIKE ? ESCAPE '\'`, likePattern(value)
}

// matchIP is match for an address column: a complete address is exact.
func matchIP(col, value string, mode matchMode) (string, any) {
	if _, err := netip.ParseAddr(value); err == nil {
		return col + " = ?", value
	}
	return match(col, value, mode)
}

// likePattern turns * and % into LIKE's wildcard and escapes the rest, case
// folded, for ESCAPE '\'.
func likePattern(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch r {
		case '*', '%':
			b.WriteByte('%')
		case '_', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
