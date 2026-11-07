// Package textmatch matches typed text against names the way every tkt search does: case and accents
// ignored, names starting with the text before names that only contain it. The Herdr mod's lib/match.js
// follows the same rule.
package textmatch

import (
	"strings"
	"unicode/utf8"
)

// accented maps the lower-case Latin letters Unicode decomposes into a base letter plus accents to that
// letter, so folding needs no normalization tables.
var accented = func() map[rune]rune {
	m := map[rune]rune{}
	for base, letters := range map[rune]string{
		'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ď", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥ",
		'i': "ìíîïĩīĭį", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľ", 'n': "ñńņň", 'o': "òóôõöōŏő", 'r': "ŕŗř",
		's': "śŝşš", 't': "ţť", 'u': "ùúûüũūŭůűų", 'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range letters {
			m[r] = base
		}
	}
	return m
}()

// Fold lowers s and drops its accents, so "Revisión" and "revision" compare equal.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(utf8.RuneCountInString(s))
	for _, r := range strings.ToLower(s) {
		if base, ok := accented[r]; ok {
			r = base
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Rank is 0 when name starts with text (or text is blank), 1 when it only contains it, -1 when it does not.
func Rank(name, text string) int {
	q := strings.TrimSpace(Fold(text))
	if q == "" {
		return 0
	}
	n := Fold(name)
	switch {
	case strings.HasPrefix(n, q):
		return 0
	case strings.Contains(n, q):
		return 1
	}
	return -1
}

// Filter keeps the items whose name matches text: those starting with it first, each group in its given order.
func Filter[T any](items []T, text string, name func(T) string) []T {
	var starts, contains []T
	for _, it := range items {
		switch Rank(name(it), text) {
		case 0:
			starts = append(starts, it)
		case 1:
			contains = append(contains, it)
		}
	}
	return append(starts, contains...)
}
