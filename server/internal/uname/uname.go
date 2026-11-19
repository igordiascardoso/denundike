// Package uname holds the rules for @logins (usernames).
//
// Decisions (documented on purpose):
//   - A login has 3 to 32 characters (the app UI limits new ones to 20): Latin letters (with accents, e.g. Brizão, ç, ã),
//     digits, "_" and ".". Other scripts are rejected so that a Cyrillic "а" cannot
//     pose as a Latin "a". Spaces, "@", emoji and combining marks are rejected.
//   - The login is stored exactly as typed (case and accents kept, for display).
//   - Uniqueness and lookups use Norm(): lowercase with accents removed. So
//     "Brizão", "brizao" and "BRIZAO" are the same login: nobody can impersonate
//     another user by dropping an accent. Typing the name with or without accent,
//     in any case, finds the same account.
package uname

import (
	"strings"
	"unicode"
)

const (
	MinLen = 3
	MaxLen = 32 // the app UI asks for 3-20; the server tolerates up to 32 (older clients/tests)
)

// Clean trims spaces and a leading "@".
func Clean(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "@")
}

// Valid reports whether s (already cleaned) is an acceptable login.
func Valid(s string) bool {
	n := 0
	alnum := false
	for _, r := range s {
		n++
		switch {
		case r == '_' || r == '.':
		case unicode.IsDigit(r) && r < 128:
			alnum = true
		case unicode.IsLetter(r) && unicode.Is(unicode.Latin, r):
			alnum = true
		default:
			return false
		}
	}
	return n >= MinLen && n <= MaxLen && alnum
}

// fold maps accented Latin letters to their plain base letter.
var fold = func() map[rune]string {
	m := map[rune]string{}
	add := func(base string, variants string) {
		for _, r := range variants {
			m[r] = base
		}
	}
	add("a", "àáâãäåāăąǎǻ")
	add("c", "çćĉċč")
	add("d", "ďđ")
	add("e", "èéêëēĕėęěȩ")
	add("g", "ĝğġģ")
	add("h", "ĥħ")
	add("i", "ìíîïĩīĭįıǐ")
	add("j", "ĵ")
	add("k", "ķ")
	add("l", "ĺļľŀł")
	add("n", "ñńņňǹ")
	add("o", "òóôõöøōŏőǒǿ")
	add("r", "ŕŗř")
	add("s", "śŝşšș")
	add("t", "ţťŧț")
	add("u", "ùúûüũūŭůűųǔ")
	add("w", "ŵ")
	add("y", "ýÿŷ")
	add("z", "źżž")
	m['ß'] = "ss"
	m['æ'] = "ae"
	m['œ'] = "oe"
	return m
}()

// Norm is the uniqueness/lookup key: lowercase, accents removed.
func Norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(Clean(s)) {
		if f, ok := fold[r]; ok {
			b.WriteString(f)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
