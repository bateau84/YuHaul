// Package fingerprint implements YuHaul's title normalization and fingerprinting.
//
// The fingerprint is a normalized, lossy token set used for fuzzy duplicate
// detection. It is intentionally one-way: the original title cannot be
// reconstructed from the fingerprint.
package fingerprint

import (
	"sort"
	"strings"
	"unicode"
)

// commonStopWords are tokens removed from titles because they carry little
// semantic value for music-title duplicate detection. The list matches the
// normalization described in the architecture.
var commonStopWords = map[string]struct{}{
	"official":   {},
	"video":      {},
	"audio":      {},
	"lyrics":     {},
	"lyric":      {},
	"ft":         {},
	"feat":       {},
	"featuring":  {},
	"hd":         {},
	"remaster":   {},
	"remastered": {},
	"extended":   {},
	"mix":        {},
	"cover":      {},
}

// IsTokenChar reports whether r is a character that can appear inside a
// fingerprint token. Letters and digits are kept; everything else becomes a
// word separator. Unicode letters are preserved.
func IsTokenChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// Normalize returns the sorted, space-delimited token set for title.
func Normalize(title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))

	var b strings.Builder
	b.Grow(len(lower))
	for _, r := range lower {
		if IsTokenChar(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}

	rawTokens := strings.Fields(b.String())
	filtered := make([]string, 0, len(rawTokens))
	seen := make(map[string]struct{}, len(rawTokens))
	for _, tok := range rawTokens {
		if _, ok := commonStopWords[tok]; ok {
			continue
		}
		if _, ok := seen[tok]; ok {
			continue
		}
		seen[tok] = struct{}{}
		filtered = append(filtered, tok)
	}

	sort.Strings(filtered)
	return strings.Join(filtered, " ")
}

// TokenSet splits a canonical fingerprint string into its sorted tokens.
// An empty string yields nil.
func TokenSet(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

// FromTitle returns the canonical fingerprint, the FTS5 token string, and a
// boolean indicating whether any tokens remain after normalization. Both the
// fingerprint and token strings are the same sorted, space-delimited token set.
func FromTitle(title string) (fingerprint string, tokens string, ok bool) {
	fp := Normalize(title)
	if fp == "" {
		return "", "", false
	}
	return fp, fp, true
}

// Compute returns the canonical fingerprint string and the token string
// suitable for FTS5 indexing.
func Compute(title string) (fingerprint string, tokens string) {
	fp := Normalize(title)
	return fp, fp
}

// Similarity computes normalized-token-set Jaccard similarity between two
// canonical fingerprint strings.
//
//	similarity(A, B) = |A ∩ B| / |A ∪ B|
func Similarity(a, b string) float64 {
	return jaccard(TokenSet(a), TokenSet(b))
}

func jaccard(a, b []string) float64 {
	setA := make(map[string]struct{}, len(a))
	for _, t := range a {
		setA[t] = struct{}{}
	}

	seenB := make(map[string]struct{}, len(b))
	intersection := 0
	for _, t := range b {
		if _, ok := seenB[t]; ok {
			continue
		}
		seenB[t] = struct{}{}
		if _, ok := setA[t]; ok {
			intersection++
		}
	}

	union := len(setA) + len(seenB) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}
