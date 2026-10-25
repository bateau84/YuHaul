package router

import (
	"strings"

	"github.com/google/uuid"
)

// ParsePublicRouteID validates a public route identifier from a request. Per
// ARS-004 the public route ID is a canonical hyphenated UUIDv4; the parser
// accepts uppercase hexadecimal in that form and returns the canonical
// lowercase spelling. Decimal integers, bare hex, braces, URNs, a non-v4
// version or variant, and any other shape are malformed — there is no numeric
// alias.
func ParsePublicRouteID(raw string) (string, bool) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", false
	}
	if parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		return "", false
	}
	canonical := parsed.String()
	// Length 36 plus case-insensitive equality with the canonical form
	// restricts acceptance to the hyphenated spelling; uuid.Parse alone would
	// also accept bare hex, braced, and URN forms.
	if len(raw) != len(canonical) || !strings.EqualFold(raw, canonical) {
		return "", false
	}
	return canonical, true
}
