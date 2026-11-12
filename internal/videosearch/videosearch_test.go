package videosearch

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const id = "dQw4w9WgXcQ"

// T1
func TestClassify(t *testing.T) {
	long200 := strings.Repeat("é", 200)
	tests := []struct {
		name string
		raw  string
		want Input
	}{
		{"empty", "", Input{Kind: KindEmpty}},
		{"blank", "  \t\n", Input{Kind: KindEmpty}},
		{"invalid utf8", "ab\xffcd", Input{Kind: KindInvalidText}},
		{"one char", "a", Input{Kind: KindTooShort}},
		{"two chars", "lo", Input{Kind: KindTooShort}},
		{"two runes four bytes", "ßx", Input{Kind: KindTooShort}},
		{"three chars", "lof", Input{Kind: KindName, Text: "lof"}},
		{"trimmed", "  lofi beats ", Input{Kind: KindName, Text: "lofi beats"}},
		{"200 multibyte runes", long200, Input{Kind: KindName, Text: long200}},
		{"201 multibyte runes", long200 + "é", Input{Kind: KindTooLong}},
		{"known id", id, Input{Kind: KindTypedID, VideoID: id, Text: id}},
		{"id shaped word", "abcdefghijk", Input{Kind: KindTypedID, VideoID: "abcdefghijk", Text: "abcdefghijk"}},
		{"id with dash and underscore", "a-b_c-d_e-f", Input{Kind: KindTypedID, VideoID: "a-b_c-d_e-f", Text: "a-b_c-d_e-f"}},
		{"ten chars is a name", "abcdefghij", Input{Kind: KindName, Text: "abcdefghij"}},
		{"twelve chars is a name", "abcdefghijkl", Input{Kind: KindName, Text: "abcdefghijkl"}},
		{"separator not at start", "see https://youtu.be/" + id, Input{Kind: KindName, Text: "see https://youtu.be/" + id}},
		{"youtu.be without scheme", "youtu.be/" + id, Input{Kind: KindName, Text: "youtu.be/" + id}},
		{"watch without scheme", "www.youtube.com/watch?v=" + id, Input{Kind: KindName, Text: "www.youtube.com/watch?v=" + id}},
		{"too long link wins over link rules", "https://www.youtube.com/watch?v=" + id + "&x=" + strings.Repeat("a", 250), Input{Kind: KindTooLong}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Classify(tc.raw))
		})
	}
}

// T2
func TestClassifyLinks(t *testing.T) {
	ok := []string{
		"https://www.youtube.com/watch?v=" + id,
		"https://www.youtube.com/watch?v=" + id + "&t=42&list=PL1#frag",
		"https://www.youtube.com/watch?list=PL1&t=9&v=" + id,
		"https://m.youtube.com/watch?v=" + id,
		"https://youtube.com/watch?v=" + id,
		"HTTPS://WWW.YOUTUBE.COM/watch?v=" + id,
		"http://www.youtube.com/watch/?v=" + id,
		"https://www.youtube.com/watch?v=" + id + "#t=1",
		"https://youtu.be/" + id,
		"https://youtu.be/" + id + "/",
		"https://youtu.be/" + id + "?t=42",
		"https://www.youtube.com/shorts/" + id,
		"https://www.youtube.com/shorts/" + id + "/",
		"https://m.youtube.com/shorts/" + id + "?feature=share",
	}
	for _, link := range ok {
		t.Run("ok "+link, func(t *testing.T) {
			assert.Equal(t, Input{Kind: KindLinkID, VideoID: id}, Classify(link))
		})
	}

	unusable := []string{
		"https://www.youtube.com/watch?list=PL123",
		"https://www.youtube.com/watch?v=a&v=b",
		"https://www.youtube.com/watch?v=" + id + "&v=" + id,
		"https://www.youtube.com/watch?v=",
		"https://www.youtube.com/watch?v=abcdefghij",   // 10
		"https://www.youtube.com/watch?v=abcdefghijkl", // 12
		"https://youtu.be/abcdefghij",
		"https://youtu.be/abcdefghijkl",
		"https://youtu.be/",
		"https://youtu.be/" + id + "/extra",
		"https://www.youtube.com/embed/" + id,
		"https://www.youtube.com/live/" + id,
		"https://www.youtube.com/Watch?v=" + id,
		"https://www.youtube.com/shorts/" + id + "/extra",
		"https://www.youtube.com/shorts/",
		"https://www.youtube.com/",
		"https://youtu.be/watch?v=" + id,
		"https://www.youtube.com/" + id,
		"ftp://youtube.com/watch?v=" + id,
		"https://www.youtube.com:8080/watch?v=" + id,
		"https://user@www.youtube.com/watch?v=" + id,
		"https://user:pw@youtu.be/" + id,
		"https://example.com/watch?v=" + id,
		"https://notyoutube.com/watch?v=" + id,
		"https://www.youtube.com.evil.com/watch?v=" + id,
		"https://[::1/watch?v=" + id, // url.Parse fails
	}
	for _, link := range unusable {
		t.Run("unusable "+link, func(t *testing.T) {
			assert.Equal(t, Input{Kind: KindUnusableLink}, Classify(link))
		})
	}

	// 250-character valid link: too_long, not a link result.
	long := "https://www.youtube.com/watch?v=" + id + "&pad=" + strings.Repeat("x", 250-len("https://www.youtube.com/watch?v="+id+"&pad="))
	require.Len(t, long, 250)
	assert.Equal(t, KindTooLong, Classify(long).Kind)
}

// T3
func TestMatcher(t *testing.T) {
	tests := []struct {
		name          string
		needle, title string
		want          bool
	}{
		{"case", "LOFI", "Lofi Beats", true},
		{"case mid title", "beats", "Lofi Beats", true},
		{"precomposed vs plain", "cafe", "Café", false},
		{"nfd title vs plain needle", "cafe", "Café", false},
		{"nfd needle matches precomposed title", "café", "Café", true},
		{"precomposed needle matches nfd title", "café", "Café", true},
		{"accent and case", "café", "CAFÉ", true},
		{"full folding", "STRASSE", "Straße", true},
		{"full folding reverse", "straße", "STRASSE", true},
		{"percent literal", "100%", "1000", false},
		{"percent matches itself", "100%", "Now 100% sure", true},
		{"underscore literal", "a_b", "axb", false},
		{"underscore matches itself", "a_b", "a_b", true},
		{"star", "a*b", "aXXb", false},
		{"star matches itself", "a*b", "a*b", true},
		{"double quote", `"hi"`, `say "hi" now`, true},
		{"single quote", "it's", "it's fine", true},
		{"backslash", `a\b`, `a\b`, true},
		{"backslash not escape", `a\b`, "ab", false},
		{"double space vs single", "a  b", "a b", false},
		{"single space vs double", "a b", "a  b", false},
		{"double space matches itself", "a  b", "a  b", true},
		{"ends before combining mark", "x", "x́y", false},
		{"ends before combining mark at end of word", "ab", "ab́", false},
		{"includes the combining mark", "x́", "x́y", true},
		{"later occurrence is fine", "x", "x́ and x", true},
		{"earlier clean occurrence", "x", "x and x́", true},
		{"needle of only a combining mark", "́", "x́", true},
		{"at end", "beats", "Lofi Beats", true},
		{"not present", "jazz", "Lofi Beats", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Contains(Key(tc.title), Key(tc.needle)))
		})
	}
}

func TestKeyAndEmptyNeedle(t *testing.T) {
	assert.Equal(t, "strasse", Key("Straße"))
	assert.Equal(t, Key("Café"), Key("CAFÉ"))
	assert.False(t, Contains("abc", ""))
	f := NewFolder()
	assert.Equal(t, Key("Lofi"), f.Key("Lofi"))
}

// T4
func TestCursor(t *testing.T) {
	n := Key("lofi")
	disc := time.Date(2025, 3, 4, 5, 6, 7, 123456789, time.UTC)

	t.Run("round trip with discovery time", func(t *testing.T) {
		in := Boundary{Dir: After, Key: SortKey{HasDisc: true, Disc: disc, VideoID: id}}
		got, err := decodeCursor(encodeCursor(n, in), n)
		require.NoError(t, err)
		assert.Equal(t, After, got.Dir)
		assert.True(t, got.Key.HasDisc)
		assert.True(t, got.Key.Disc.Equal(disc))
		assert.Equal(t, id, got.Key.VideoID)
	})
	t.Run("round trip without discovery time", func(t *testing.T) {
		in := Boundary{Dir: Before, Key: SortKey{VideoID: id}}
		c := encodeCursor(n, in)
		got, err := decodeCursor(c, n)
		require.NoError(t, err)
		assert.Equal(t, in, got)
		raw, _ := base64.RawURLEncoding.DecodeString(c)
		assert.Contains(t, string(raw), `"d":null`)
	})
	t.Run("holds no search text", func(t *testing.T) {
		c := encodeCursor(n, Boundary{Dir: After, Key: SortKey{VideoID: id}})
		raw, _ := base64.RawURLEncoding.DecodeString(c)
		assert.NotContains(t, string(raw), "lofi")
		assert.NotContains(t, c, "=")
	})

	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	h := needleHash(n)
	good := fmt.Sprintf(`{"v":1,"h":%q,"dir":"after","d":null,"id":%q}`, h, id)
	_, err := decodeCursor(enc(good), n)
	require.NoError(t, err, "fixture must be valid")

	bad := map[string]string{
		"not base64":      "!!!",
		"padded base64":   enc(good) + "=",
		"not json":        enc("nope"),
		"empty":           "",
		"tampered":        enc(strings.Replace(good, `"after"`, `"afte"`, 1)),
		"wrong hash":      enc(fmt.Sprintf(`{"v":1,"h":%q,"dir":"after","d":null,"id":%q}`, needleHash(Key("jazz")), id)),
		"wrong version":   enc(fmt.Sprintf(`{"v":2,"h":%q,"dir":"after","d":null,"id":%q}`, h, id)),
		"missing version": enc(fmt.Sprintf(`{"h":%q,"dir":"after","d":null,"id":%q}`, h, id)),
		"wrong dir":       enc(fmt.Sprintf(`{"v":1,"h":%q,"dir":"around","d":null,"id":%q}`, h, id)),
		"bad id":          enc(fmt.Sprintf(`{"v":1,"h":%q,"dir":"after","d":null,"id":"short"}`, h)),
		"unknown member":  enc(fmt.Sprintf(`{"v":1,"h":%q,"dir":"after","d":null,"id":%q,"x":1}`, h, id)),
		"trailing data":   enc(good + good),
		"over 512 bytes":  enc(fmt.Sprintf(`{"v":1,"h":%q,"dir":"after","d":null,"id":%q}`, h, id) + strings.Repeat(" ", 600)),
	}
	for name, c := range bad {
		t.Run("invalid "+name, func(t *testing.T) {
			_, err := decodeCursor(c, n)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrInvalidCursor)
		})
	}
	t.Run("different search text", func(t *testing.T) {
		c := encodeCursor(n, Boundary{Dir: After, Key: SortKey{VideoID: id}})
		_, err := decodeCursor(c, Key("jazz"))
		assert.ErrorIs(t, err, ErrInvalidCursor)
	})
}

func TestParsePage(t *testing.T) {
	for s, want := range map[string]int{"1": 1, "2": 2, "0007": 7, "9999999": 9999999} {
		got, err := ParsePage(s)
		require.NoError(t, err, s)
		assert.Equal(t, want, got, s)
	}
	for _, s := range []string{"", "0", "00", "-1", "+1", "x", "1x", "1.5", " 1", "12345678", "٣"} {
		_, err := ParsePage(s)
		assert.ErrorIs(t, err, ErrInvalidPage, "%q", s)
	}
}

// T5
func TestSortOrder(t *testing.T) {
	d := func(day int) SortKey {
		return SortKey{HasDisc: true, Disc: time.Date(2025, 1, day, 0, 0, 0, 0, time.UTC)}
	}
	k := func(s SortKey, id string) SortKey { s.VideoID = id; return s }
	keys := []SortKey{
		{VideoID: "bbbbbbbbbbb"}, // no discovery time
		k(d(1), "ccccccccccc"),
		{VideoID: "aaaaaaaaaaa"},
		k(d(3), "zzzzzzzzzzz"),
		k(d(3), "aaaaaaaaaaa"), // tie broken by ID
		k(d(2), "mmmmmmmmmmm"),
	}
	SortKeys(keys)
	var got []string
	for _, key := range keys {
		got = append(got, key.VideoID)
	}
	assert.Equal(t, []string{
		"aaaaaaaaaaa", "zzzzzzzzzzz", "mmmmmmmmmmm", "ccccccccccc", // dated, newest first, ties by ID
		"aaaaaaaaaaa", "bbbbbbbbbbb", // undated last, by ID
	}, got)
	assert.True(t, keys[0].HasDisc && keys[3].HasDisc && !keys[4].HasDisc)

	assert.Zero(t, k(d(1), "x").Compare(k(d(1), "x")))
	// Equal instants in different zones tie.
	other := SortKey{HasDisc: true, Disc: d(1).Disc.In(time.FixedZone("x", 3600)), VideoID: "x"}
	assert.Zero(t, k(d(1), "x").Compare(other))
}

func sortedKeys(n int) []SortKey {
	keys := make([]SortKey, n)
	for i := range keys {
		keys[i] = SortKey{HasDisc: true, Disc: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(-i) * time.Hour), VideoID: fmt.Sprintf("v%010d", i)}
	}
	SortKeys(keys)
	return keys
}

func TestPaging130(t *testing.T) {
	keys := sortedKeys(130)
	n := Key("q")

	type page struct {
		w    Window
		info PageInfo
	}
	var pages []page
	// Walk forward with cursors.
	w := Window{0, PageSize}
	for num := 1; ; num++ {
		info := NewPageInfo(len(keys), w, num)
		pages = append(pages, page{w, info})
		if !info.HasNext {
			break
		}
		last := keys[w.End-1]
		b, err := decodeCursor(encodeCursor(n, Boundary{Dir: After, Key: last}), n)
		require.NoError(t, err)
		w = CursorWindow(keys, b)
	}
	require.Len(t, pages, 3)
	want := []struct{ first, last int }{{1, 50}, {51, 100}, {101, 130}}
	covered := 0
	for i, p := range pages {
		assert.Equal(t, 130, p.info.Total)
		assert.Equal(t, 3, p.info.TotalPages)
		assert.Equal(t, i+1, p.info.Number)
		assert.Equal(t, want[i].first, p.info.FirstPosition)
		assert.Equal(t, want[i].last, p.info.LastPosition)
		assert.Equal(t, i > 0, p.info.HasPrev)
		assert.Equal(t, i < 2, p.info.HasNext)
		covered += p.w.End - p.w.Start
		// Page jump gives the same window as the cursor walk.
		assert.Equal(t, p.w, PageWindow(130, i+1))
	}
	assert.Equal(t, 130, covered)

	// Walk backward from the last page with "before" cursors.
	w = pages[2].w
	for num := 3; num > 1; num-- {
		first := keys[w.Start]
		b, err := decodeCursor(encodeCursor(n, Boundary{Dir: Before, Key: first}), n)
		require.NoError(t, err)
		w = CursorWindow(keys, b)
		assert.Equal(t, pages[num-2].w, w)
	}

	// A page past the end is empty and reports the requested number.
	w = PageWindow(130, 4)
	assert.Equal(t, Window{130, 130}, w)
	info := NewPageInfo(130, w, 4)
	assert.Equal(t, 4, info.Number)
	assert.Equal(t, 130, info.Total)
	assert.Zero(t, info.FirstPosition)
	assert.Zero(t, info.LastPosition)
}

func TestPageMathEdges(t *testing.T) {
	// No matches.
	info := NewPageInfo(0, PageWindow(0, 1), 1)
	assert.Equal(t, PageInfo{Total: 0, TotalPages: 1, Number: 1}, info)

	// Exactly one full page.
	info = NewPageInfo(50, PageWindow(50, 1), 1)
	assert.Equal(t, 1, info.TotalPages)
	assert.False(t, info.HasNext)
	assert.Equal(t, 50, info.LastPosition)

	// 51 items: two pages.
	assert.Equal(t, 2, NewPageInfo(51, PageWindow(51, 1), 1).TotalPages)
	assert.Equal(t, Window{50, 51}, PageWindow(51, 2))
}

func TestCursorWindowBoundaryNeedNotExist(t *testing.T) {
	keys := sortedKeys(120)
	// Boundary between keys[59] and keys[60]: neither the boundary item nor any
	// key equal to it exists in the list.
	gap := keys[59]
	gap.Disc = gap.Disc.Add(-time.Minute)
	gap.VideoID = "gap"
	require.Equal(t, -1, keys[59].Compare(gap))
	require.Equal(t, 1, keys[60].Compare(gap))

	assert.Equal(t, Window{60, 110}, CursorWindow(keys, Boundary{Dir: After, Key: gap}))
	assert.Equal(t, Window{10, 60}, CursorWindow(keys, Boundary{Dir: Before, Key: gap}))

	// A boundary that is the last item has nothing after it.
	assert.Equal(t, Window{120, 120}, CursorWindow(keys, Boundary{Dir: After, Key: keys[119]}))
	// A boundary that is the first item has nothing before it.
	assert.Equal(t, Window{0, 0}, CursorWindow(keys, Boundary{Dir: Before, Key: keys[0]}))
	// Undated boundary sorts after every dated key.
	assert.Equal(t, Window{70, 120}, CursorWindow(keys, Boundary{Dir: Before, Key: SortKey{VideoID: "zzzzzzzzzzz"}}))
}
