package videosearch

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// PageSize is the fixed number of items on a result page.
const PageSize = 50

const (
	maxCursorBytes = 512
	maxPageDigits  = 7
)

// Direction says on which side of the boundary item a cursor page lies.
type Direction string

const (
	After  Direction = "after"
	Before Direction = "before"
)

// Errors for invalid paging input. The server maps them to 400 codes (§7.5).
var (
	ErrInvalidCursor = errors.New("invalid cursor")
	ErrInvalidPage   = errors.New("invalid page")
)

// SortKey orders matches (ARS-001 §7.1): items with a discovery time first,
// newest first, then by video ID in byte order.
type SortKey struct {
	HasDisc bool
	Disc    time.Time
	VideoID string
}

// Compare returns a negative number when a comes before b, positive when after,
// and zero when the keys are equal.
func (a SortKey) Compare(b SortKey) int {
	switch {
	case a.HasDisc && !b.HasDisc:
		return -1
	case !a.HasDisc && b.HasDisc:
		return 1
	case a.HasDisc && !a.Disc.Equal(b.Disc):
		if a.Disc.After(b.Disc) {
			return -1
		}
		return 1
	}
	return strings.Compare(a.VideoID, b.VideoID)
}

// SortKeys sorts keys into result order in place.
func SortKeys(keys []SortKey) {
	slices.SortFunc(keys, SortKey.Compare)
}

// Boundary is the item a cursor names (ARS-001 §7.2).
type Boundary struct {
	Dir Direction
	Key SortKey
}

type cursorWire struct {
	V   int       `json:"v"`
	H   string    `json:"h"`
	Dir Direction `json:"dir"`
	D   *int64    `json:"d"`
	ID  string    `json:"id"`
}

// needleHash is the first 8 bytes of SHA-256 of the needle key, hex encoded.
func needleHash(needleKey string) string {
	sum := sha256.Sum256([]byte(needleKey))
	return hex.EncodeToString(sum[:8])
}

// encodeCursor builds the opaque cursor. It holds a hash of needleKey, never
// the search text or a title.
func encodeCursor(needleKey string, b Boundary) string {
	w := cursorWire{V: 1, H: needleHash(needleKey), Dir: b.Dir, ID: b.Key.VideoID}
	if b.Key.HasDisc {
		n := b.Key.Disc.UnixNano()
		w.D = &n
	}
	raw, _ := json.Marshal(w) // fixed struct of plain types; cannot fail
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor validates s against the current needleKey (ARS-001 §7.5). Every
// failure wraps ErrInvalidCursor.
func decodeCursor(s, needleKey string) (Boundary, error) {
	bad := func(why string) (Boundary, error) {
		return Boundary{}, errors.Join(ErrInvalidCursor, errors.New(why))
	}
	if len(s) > maxCursorBytes {
		return bad("too long")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return bad("not base64url")
	}
	var w cursorWire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return bad("not cursor JSON")
	}
	if dec.More() {
		return bad("trailing data")
	}
	switch {
	case w.V != 1:
		return bad("version")
	case w.Dir != After && w.Dir != Before:
		return bad("direction")
	case !ValidVideoID(w.ID):
		return bad("id")
	case w.H != needleHash(needleKey):
		return bad("search text changed")
	}
	b := Boundary{Dir: w.Dir, Key: SortKey{VideoID: w.ID}}
	if w.D != nil {
		b.Key.HasDisc = true
		b.Key.Disc = time.Unix(0, *w.D).UTC()
	}
	return b, nil
}

// ParsePage parses the page parameter: a positive decimal integer of at most
// 7 digits (ARS-001 §7.5).
func ParsePage(s string) (int, error) {
	if s == "" || len(s) > maxPageDigits {
		return 0, ErrInvalidPage
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalidPage
		}
		n = n*10 + int(c-'0')
	}
	if n < 1 {
		return 0, ErrInvalidPage
	}
	return n, nil
}

// Window is the half-open range [Start, End) of positions in the sorted list.
type Window struct{ Start, End int }

// PageWindow returns the window of page n (1-based) of total items. A page past
// the end gives an empty window at total.
func PageWindow(total, n int) Window {
	start := min((n-1)*PageSize, total)
	return Window{start, min(start+PageSize, total)}
}

// CursorWindow returns the window of the page that lies after or before the
// boundary in the sorted keys. Comparison is by key alone, so the boundary item
// need not exist.
func CursorWindow(sorted []SortKey, b Boundary) Window {
	if b.Dir == After {
		// First index whose key is strictly after the boundary.
		start, _ := slices.BinarySearchFunc(sorted, b.Key, func(e, t SortKey) int {
			if e.Compare(t) <= 0 {
				return -1
			}
			return 1
		})
		return Window{start, min(start+PageSize, len(sorted))}
	}
	// End is the count of keys strictly before the boundary.
	end, _ := slices.BinarySearchFunc(sorted, b.Key, SortKey.Compare)
	return Window{max(end-PageSize, 0), end}
}

// PageInfo is the position data of a Page (ARS-001 §4.1).
type PageInfo struct {
	Total      int
	TotalPages int
	Number     int
	// FirstPosition and LastPosition are 1-based; both are 0 when the page is
	// empty.
	FirstPosition int
	LastPosition  int
	HasNext       bool
	HasPrev       bool
}

// NewPageInfo computes the position data for window w of total items.
// requested is the page number to report when the window is empty.
func NewPageInfo(total int, w Window, requested int) PageInfo {
	info := PageInfo{
		Total:      total,
		TotalPages: max(1, (total+PageSize-1)/PageSize),
		Number:     requested,
		HasNext:    w.End < total,
		HasPrev:    w.Start > 0,
	}
	if w.End > w.Start {
		info.FirstPosition = w.Start + 1
		info.LastPosition = w.End
		info.Number = w.Start/PageSize + 1
	}
	return info
}
