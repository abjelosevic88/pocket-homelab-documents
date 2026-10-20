// Package match decides which tags, correspondent, document type and storage
// path a document gets: by the matching rules Paperless users know (any /
// all words, exact phrase, regular expression, fuzzy) and by a classifier
// that learns from the documents a person already filed ("auto").
package match

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Matches reports whether a rule (match text + algorithm) fits the text.
// Auto (6) is not decided here but by the Classifier.
func Matches(m store.Matchable, text string) bool {
	match := strings.TrimSpace(m.Match)
	if match == "" || m.MatchingAlgorithm == store.MatchNone || m.MatchingAlgorithm == store.MatchAuto {
		return false
	}
	if m.IsInsensitive {
		text, match = strings.ToLower(text), strings.ToLower(match)
	}
	switch m.MatchingAlgorithm {
	case store.MatchAny:
		for _, w := range strings.Fields(match) {
			if hasWord(text, w) {
				return true
			}
		}
		return false
	case store.MatchAll:
		for _, w := range strings.Fields(match) {
			if !hasWord(text, w) {
				return false
			}
		}
		return true
	case store.MatchLiteral:
		return hasWord(text, match)
	case store.MatchRegex:
		flags := ""
		if m.IsInsensitive {
			flags = "(?i)"
		}
		re, err := regexp.Compile(flags + m.Match)
		if err != nil {
			return false
		}
		return re.MatchString(text)
	case store.MatchFuzzy:
		return fuzzyContains(text, match, 0.9)
	}
	return false
}

// hasWord reports whether needle appears in text at word boundaries.
func hasWord(text, needle string) bool {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return false
	}
	idx := 0
	for {
		i := strings.Index(text[idx:], needle)
		if i < 0 {
			return false
		}
		start := idx + i
		end := start + len(needle)
		before := start == 0 || !isWordRune(lastRune(text[:start]))
		after := end >= len(text) || !isWordRune(firstRune(text[end:]))
		if before && after {
			return true
		}
		idx = start + 1
		if idx >= len(text) {
			return false
		}
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

func lastRune(s string) rune {
	var r rune
	for _, x := range s {
		r = x
	}
	return r
}

func firstRune(s string) rune {
	for _, x := range s {
		return x
	}
	return 0
}

// fuzzyContains is a partial-ratio check: does any window of the text, the
// size of the needle, match it with similarity ≥ threshold?
func fuzzyContains(text, needle string, threshold float64) bool {
	n := []rune(strings.Join(strings.Fields(needle), " "))
	t := []rune(strings.Join(strings.Fields(text), " "))
	if len(n) == 0 || len(t) == 0 {
		return false
	}
	if len(t) <= len(n) {
		return similarity(t, n) >= threshold
	}
	maxDist := int(float64(len(n)) * (1 - threshold))
	// Slide a window over the text; the needle's first rune helps skip quickly.
	for i := 0; i+len(n) <= len(t); i++ {
		w := t[i : i+len(n)]
		if levenshtein(w, n, maxDist) <= maxDist {
			return true
		}
	}
	// A window one rune longer or shorter also counts (insertions at the edges).
	for _, size := range []int{len(n) - 1, len(n) + 1} {
		if size <= 0 || size > len(t) {
			continue
		}
		for i := 0; i+size <= len(t); i++ {
			if levenshtein(t[i:i+size], n, maxDist) <= maxDist {
				return true
			}
		}
	}
	return false
}

func similarity(a, b []rune) float64 {
	d := levenshtein(a, b, len(a)+len(b))
	m := len(a)
	if len(b) > m {
		m = len(b)
	}
	if m == 0 {
		return 1
	}
	return 1 - float64(d)/float64(m)
}

// levenshtein with an early exit once every cell of a row exceeds limit.
func levenshtein(a, b []rune, limit int) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			v := prev[j] + 1
			if cur[j-1]+1 < v {
				v = cur[j-1] + 1
			}
			if prev[j-1]+cost < v {
				v = prev[j-1] + cost
			}
			cur[j] = v
			if v < rowMin {
				rowMin = v
			}
		}
		if rowMin > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
