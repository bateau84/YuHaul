package videosearch

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// Folder computes match keys (ARS-001 §5). A cases.Caser is not safe for
// concurrent use, so create one Folder per request and do not share it.
type Folder struct {
	caser cases.Caser
}

// NewFolder returns a Folder for one request.
func NewFolder() *Folder { return &Folder{caser: cases.Fold()} }

// Key returns NFC(fold(NFC(s))) with full Unicode case folding (ß → ss).
func (f *Folder) Key(s string) string {
	return norm.NFC.String(f.caser.String(norm.NFC.String(s)))
}

// Key is a convenience for one-off use. Hot loops use a Folder.
func Key(s string) string { return NewFolder().Key(s) }

// Contains reports whether the key needle occurs in the key title as a literal
// substring that does not end before a combining mark (Unicode category M).
// Both arguments must be Key results. An empty needle never matches.
func Contains(titleKey, needleKey string) bool {
	if needleKey == "" {
		return false
	}
	for off := 0; ; {
		i := strings.Index(titleKey[off:], needleKey)
		if i < 0 {
			return false
		}
		end := off + i + len(needleKey)
		if end == len(titleKey) {
			return true
		}
		if r, _ := utf8.DecodeRuneInString(titleKey[end:]); !unicode.Is(unicode.M, r) {
			return true
		}
		off += i + 1
	}
}
