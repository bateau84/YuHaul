// Package titlekey computes the title match keys shared by video search and
// the metadata search index (video-search ARS-001 §1.2, §10.6). It is a leaf
// package: it imports only the standard library and golang.org/x/text.
package titlekey

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// keyRevision is raised whenever the code of Key changes (ARS-001 §10.6).
const keyRevision = 1

// Version identifies the key algorithm as "<rev>/<unicode>", where <unicode>
// is the Unicode version of the bundled golang.org/x/text. Index entries built
// with another version may miss true matches.
func Version() string { return strconv.Itoa(keyRevision) + "/" + norm.Version }

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
