package duplicate

import "github.com/bateau84/YuHaul/internal/fingerprint"

// Jaccard computes normalized-token-set Jaccard similarity:
//
//	similarity(A, B) = |A ∩ B| / |A ∪ B|
//
// Both slices are treated as sets; duplicate tokens in either slice do not
// affect the result. The returned value is in the range [0.0, 1.0].
func Jaccard(a, b []string) float64 {
	return fingerprint.Similarity(joinTokens(a), joinTokens(b))
}
