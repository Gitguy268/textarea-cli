// Package note is the file a note becomes, ported from textarea's
// lib/front-matter.js, lib/filename.js and lib/stats.js.
//
// The browser app, this terminal app and anything else pointed at the same
// folder have to write the same bytes, or each pass would look like an edit
// to the others. So the helpers in this file reproduce the JavaScript
// behaviour the originals lean on - String.prototype.trim, the \s class,
// JSON.stringify - rather than Go's nearest equivalent, which differs at the
// edges (U+0085, U+FEFF, HTML escaping).
package note

import (
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// IsJSSpace is JavaScript's \s: WhiteSpace plus LineTerminator.
func IsJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// JSTrim is String.prototype.trim.
func JSTrim(s string) string {
	return strings.TrimFunc(s, IsJSSpace)
}

// JSONString is JSON.stringify for a string: quotes, backslashes and control
// characters escaped, nothing else. Go's encoder also escapes <, >, & and
// U+2028/9, which JavaScript does not.
func JSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				h := strconv.FormatInt(int64(r), 16)
				if len(h) < 2 {
					b.WriteByte('0')
				}
				b.WriteString(h)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// utf8LenOfUnits is TextEncoder's byte length for a run of UTF-16 code units:
// a lone surrogate becomes U+FFFD, three bytes.
func utf8LenOfUnits(units []uint16) int {
	n := 0
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch {
		case u < 0x80:
			n++
		case u < 0x800:
			n += 2
		case u >= 0xd800 && u < 0xdc00 && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] < 0xe000:
			n += 4
			i++
		default:
			n += 3
		}
	}
	return n
}

func units(s string) []uint16 { return utf16.Encode([]rune(s)) }

func fromUnits(u []uint16) string { return string(utf16.Decode(u)) }

// CodePoints is [...s].length.
func CodePoints(s string) int { return utf8.RuneCountInString(s) }
