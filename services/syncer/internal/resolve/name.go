// Package resolve decides whether a place arriving from a source is one the catalog already has.
package resolve

import (
	"strings"
	"unicode"
)

var stopWords = map[string]bool{
	"ооо": true, "ао": true, "зао": true, "ип": true,
	"гбу": true, "мбу": true, "гау": true, "мау": true, "гбук": true, "мбук": true, "гаук": true,
	"фок": true, "им": true, "музей": true, "стадион": true,
}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Clean drops legal forms and generic words so that two names of one venue compare on what
// distinguishes it; a name made only of such words stays whole.
func Clean(name string) string {
	all := words(name)
	kept := make([]string, 0, len(all))
	for _, w := range all {
		if !stopWords[w] {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		kept = all
	}
	return strings.Join(kept, " ")
}

// trigrams follows pg_trgm: every word is padded with two spaces before and one after.
func trigrams(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, w := range words(s) {
		r := []rune("  " + w + " ")
		for i := 0; i+3 <= len(r); i++ {
			set[string(r[i:i+3])] = struct{}{}
		}
	}
	return set
}

// Trigram is pg_trgm's similarity: shared trigrams over all distinct trigrams of both names.
func Trigram(a, b string) float64 {
	x, y := trigrams(a), trigrams(b)
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	shared := 0
	for t := range x {
		if _, ok := y[t]; ok {
			shared++
		}
	}
	return float64(shared) / float64(len(x)+len(y)-shared)
}
