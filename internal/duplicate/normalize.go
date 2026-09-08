package duplicate

import (
	"strings"

	"github.com/bateau84/YuHaul/internal/fingerprint"
)

// Fingerprint is the normalized, lossy representation of a title used for
// duplicate detection.
type Fingerprint struct {
	// Canonical is the sorted, space-joined token set stored in the
	// title_fingerprint.fingerprint column.
	Canonical string
	// Tokens is the de-duplicated, sorted token set. It is also stored in
	// title_fingerprint.fingerprint_tokens for FTS5 indexing.
	Tokens []string
}

// Normalize transforms a raw title into a fingerprint per architecture
// §Fingerprint Design. The implementation delegates to the shared
// internal/fingerprint package so that migration, discovery, and duplicate
// detection all produce identical fingerprints (INV-6).
func Normalize(title string) Fingerprint {
	canonical := fingerprint.Normalize(title)
	return Fingerprint{
		Canonical: canonical,
		Tokens:    fingerprint.TokenSet(canonical),
	}
}

// joinTokens reconstructs a canonical fingerprint string from a token slice.
// It is a helper for Jaccard so that the canonical similarity implementation
// in internal/fingerprint is the single source of truth.
func joinTokens(tokens []string) string {
	return strings.Join(tokens, " ")
}
