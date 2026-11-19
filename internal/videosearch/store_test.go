package videosearch

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/titlekey"
	"github.com/stretchr/testify/require"
)

var storeNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func openStoreDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := db.Exec(q, args...)
	require.NoError(t, err)
}

// seedMeta inserts a transient_metadata row; nil expiredAt means no marker.
func seedMeta(t *testing.T, db *sql.DB, id, title string, active bool, fetched, expires time.Time, expiredAt any) {
	t.Helper()
	var ttl any = title
	if title == "NULL" {
		ttl = nil
	}
	mustExec(t, db, `INSERT INTO transient_metadata (video_id, title, channel_name, fetched_at, expires_at, expired_at, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, id, ttl, "chan "+id, fetched, expires, expiredAt, active)
}

func seedVideo(t *testing.T, db *sql.DB, id string, disc time.Time) {
	t.Helper()
	mustExec(t, db, `INSERT INTO video (video_id, channel_id, discovered_at, processing_state) VALUES (?, 'UC1', ?, 'discovered')`, id, disc)
}

func snapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out string
	for _, tbl := range []string{"transient_metadata", "video", "title_fingerprint"} {
		rows, err := db.Query(`SELECT * FROM ` + tbl + ` ORDER BY 1`)
		require.NoError(t, err)
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			out += tbl + ":"
			for _, v := range vals {
				out += " " + toStr(v)
			}
			out += "\n"
		}
		require.NoError(t, rows.Err())
		rows.Close()
	}
	return out
}

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	default:
		return fmt.Sprint(x)
	}
}

func TestScanMatchesUsesRunDetailHeldRuleAndWritesNothing(t *testing.T) {
	db := openStoreDB(t)
	h, d := time.Hour, 24*time.Hour
	// Held, with a video row.
	seedMeta(t, db, "held-video", "Lofi Beats", true, storeNow.Add(-h), storeNow.Add(h), nil)
	seedVideo(t, db, "held-video", storeNow.Add(-d))
	// Held, imported reference with no video row.
	seedMeta(t, db, "held-import", "lofi mix", true, storeNow.Add(-h), storeNow.Add(h), nil)
	mustExec(t, db, `INSERT INTO title_fingerprint (video_id, fingerprint, source) VALUES ('held-import', 'fp', 'user_import')`)
	// Not held: expired by deadline but not swept, marker, inactive, future fetch, blank, no owner.
	seedMeta(t, db, "past-deadline", "lofi old", true, storeNow.Add(-2*h), storeNow.Add(-h), nil)
	seedVideo(t, db, "past-deadline", storeNow)
	seedMeta(t, db, "marker", "lofi marker", true, storeNow.Add(-h), storeNow.Add(h), storeNow.Add(-time.Minute))
	seedVideo(t, db, "marker", storeNow)
	seedMeta(t, db, "inactive", "lofi inactive", false, storeNow.Add(-h), storeNow.Add(h), nil)
	seedVideo(t, db, "inactive", storeNow)
	seedMeta(t, db, "future-fetch", "lofi future", true, storeNow.Add(h), storeNow.Add(2*h), nil)
	seedVideo(t, db, "future-fetch", storeNow)
	seedMeta(t, db, "blank", "  ", true, storeNow.Add(-h), storeNow.Add(h), nil)
	seedVideo(t, db, "blank", storeNow)
	seedMeta(t, db, "orphan", "lofi orphan", true, storeNow.Add(-h), storeNow.Add(h), nil)

	before := snapshot(t, db)
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()

	f := titlekey.NewFolder()
	got, cutoff, err := ScanMatches(context.Background(), tx, f, f.Key("LOFI"), func() time.Time { return storeNow })
	require.NoError(t, err)
	require.Equal(t, storeNow, cutoff)
	slices.SortFunc(got, func(a, b Match) int { return a.Key.Compare(b.Key) })
	var ids []string
	for _, m := range got {
		ids = append(ids, m.Key.VideoID)
	}
	require.Equal(t, []string{"held-video", "held-import"}, ids, "discovered first, then no-discovery by ID")

	// Agreement with ReadLocalDisplay (ARS-001 T6) on every seeded row.
	manager := metadata.NewManager(metadata.NewStore(db), nil, nil, metadata.WithNow(func() time.Time { return storeNow }))
	all := []string{"held-video", "held-import", "past-deadline", "marker", "inactive", "future-fetch", "blank", "orphan"}
	display, err := manager.ReadLocalDisplay(context.Background(), all)
	require.NoError(t, err)
	meta, err := ProjectMetadata(context.Background(), tx, all, cutoff)
	require.NoError(t, err)
	for _, id := range all {
		wantHeld := display[id].TitleAvailability == "current_local"
		require.Equal(t, wantHeld, meta[id].TitleAvailability == Held, id)
		require.Equal(t, display[id].ChannelNameAvailability == "current_local", meta[id].ChannelAvailability == Held, id)
	}
	require.Equal(t, Expired, meta["past-deadline"].TitleAvailability)
	require.Equal(t, Expired, meta["marker"].TitleAvailability)
	require.Equal(t, Withheld, meta["inactive"].TitleAvailability)
	require.Equal(t, Withheld, meta["future-fetch"].TitleAvailability)
	require.Equal(t, NeverFetched, meta["blank"].TitleAvailability)
	require.Nil(t, meta["marker"].Title)
	require.Equal(t, "Lofi Beats", *meta["held-video"].Title)

	missing, err := ProjectMetadata(context.Background(), tx, []string{"never-seen"}, cutoff)
	require.NoError(t, err)
	require.Equal(t, NeverFetched, missing["never-seen"].TitleAvailability)
	require.Equal(t, NeverFetched, missing["never-seen"].ChannelAvailability)

	require.NoError(t, tx.Rollback())
	require.Equal(t, before, snapshot(t, db), "scan must not write or change expiry")
}

func TestScanMatchesStopsOnCancelledContext(t *testing.T) {
	db := openStoreDB(t)
	for i := 0; i < 2*ctxCheckEvery; i++ {
		id := string(rune('a'+i%26)) + "-" + time.Duration(i).String()
		seedMeta(t, db, id, "lofi", true, storeNow.Add(-time.Hour), storeNow.Add(time.Hour), nil)
		seedVideo(t, db, id, storeNow)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	f := titlekey.NewFolder()
	_, _, err = ScanMatches(ctx, tx, f, f.Key("lofi"), func() time.Time { return storeNow })
	require.Error(t, err)
}

// ---- Indexed search path (ARS-001 §6.1, §10.7; tests D1, D2) ----------------

// D2: the §10.7 rule, first as a pure function, then through the real service
// against the real index state.
func TestUseIndexRule_D2(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready bool
		n     string
		want  bool
	}{
		{"not built", false, "lofi", false},
		{"ready, 4 code points", true, "lofi", true},
		{"ready, exactly 3", true, "lof", true},
		{"ready, 2 code points", true, "lo", false},
		{"ready, 3 bytes but 2 code points", true, "éx", false},
		{"ready, NUL", true, "lo\x00fi", false},
		{"ready, leading NUL", true, "\x00lofi", false},
		{"ready, multi-byte", true, "ßßß", true},
	} {
		require.Equal(t, tc.want, useIndex(tc.ready, tc.n), tc.name)
	}
}

func setKeyVersion(t *testing.T, db *sql.DB, v any) {
	t.Helper()
	mustExec(t, db, `UPDATE title_search_state SET key_version = ? WHERE id = 1`, v)
}

func searchPath(t *testing.T, svc *Service, q string) (SearchResult, Path) {
	t.Helper()
	res, err := svc.Search(context.Background(), q, Paging{})
	require.NoError(t, err)
	require.Equal(t, "results", res.Kind)
	return res, res.Stats.Path
}

func TestSearchChoosesPathByIndexState_D2(t *testing.T) {
	db := openStoreDB(t)
	seedMeta(t, db, "held-video1", "Lofi Beats", true, storeNow.Add(-time.Hour), storeNow.Add(time.Hour), nil)
	seedVideo(t, db, "held-video1", storeNow)
	svc := NewService(db, func() time.Time { return storeNow })

	// Not built: store.Open has not built the index in this build.
	res, path := searchPath(t, svc, "lofi")
	require.Equal(t, PathScan, path, "index not built")
	require.Equal(t, 1, res.Results.Total)

	_, err := metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)
	res, path = searchPath(t, svc, "lofi")
	require.Equal(t, PathIndexed, path)
	require.Equal(t, 1, res.Results.Total)
	require.Equal(t, 1, res.Stats.Candidates)
	require.Equal(t, 1, res.Stats.Matches)

	// 3 typed characters whose key has 2 code points (e + U+0301 + x → éx).
	_, path = searchPath(t, svc, "e\u0301x")
	require.Equal(t, PathScan, path, "2-code-point key")
	// A 3-code-point key stays indexed.
	_, path = searchPath(t, svc, "lof")
	require.Equal(t, PathIndexed, path)
	// NUL in the key goes to the scan.
	res, path = searchPath(t, svc, "lo\x00fi")
	require.Equal(t, PathScan, path, "U+0000")
	require.Zero(t, res.Results.Total)

	// A different key version: scan, with the same answer.
	setKeyVersion(t, db, "0/x")
	res, path = searchPath(t, svc, "lofi")
	require.Equal(t, PathScan, path, "key version differs")
	require.Equal(t, 1, res.Results.Total)

	// A NULL key version (never built) is not ready either.
	setKeyVersion(t, db, nil)
	_, path = searchPath(t, svc, "lofi")
	require.Equal(t, PathScan, path)

	setKeyVersion(t, db, titlekey.Version())
	_, path = searchPath(t, svc, "lofi")
	require.Equal(t, PathIndexed, path)
	mustExec(t, db, `UPDATE title_search_state SET built_at = NULL WHERE id = 1`)
	_, path = searchPath(t, svc, "lofi")
	require.Equal(t, PathScan, path, "built_at unset")
}

func TestIndexedQueryPlanStartsAtIndex_D2(t *testing.T) {
	db := openStoreDB(t)
	rows, err := db.Query(`EXPLAIN QUERY PLAN `+indexedCandidateSQL, ftsPhrase("lofi"))
	require.NoError(t, err)
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		details = append(details, detail)
	}
	require.NoError(t, rows.Err())
	require.NotEmpty(t, details)
	require.True(t, strings.HasPrefix(details[0], "SCAN title_search VIRTUAL TABLE INDEX"), "plan: %v", details)
	require.Contains(t, details[0], ":M", "the MATCH constraint drives the scan")
	for _, d := range details {
		require.NotContains(t, d, "SCAN m", "transient_metadata must not be scanned: %v", details)
		require.NotContains(t, d, "SCAN transient_metadata", "%v", details)
	}
}

// An indexed-query error is returned, never answered from the scan (§10.7).
func TestIndexedQueryErrorHasNoScanFallback(t *testing.T) {
	db := openStoreDB(t)
	seedMeta(t, db, "held-video1", "Lofi Beats", true, storeNow.Add(-time.Hour), storeNow.Add(time.Hour), nil)
	seedVideo(t, db, "held-video1", storeNow)
	_, err := metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)

	// Damage the index so the indexed query fails while the state row still
	// says ready.
	mustExec(t, db, `DROP TRIGGER transient_metadata_search_purge_insert`)
	mustExec(t, db, `DROP TRIGGER transient_metadata_search_purge_update`)
	mustExec(t, db, `DROP TRIGGER transient_metadata_search_purge_delete`)
	mustExec(t, db, `DROP TABLE title_search_doc`)

	svc := NewService(db, func() time.Time { return storeNow })
	_, err = svc.Search(context.Background(), "lofi", Paging{})
	require.Error(t, err, "a damaged index must fail, not fall back to the scan")
	require.Contains(t, err.Error(), "indexed candidates")
}

// d1Corpus seeds the D1 corpus and reconciles the index. It returns the titles
// of every seeded row so needles can be cut from them.
func d1Corpus(t *testing.T, db *sql.DB, rng *rand.Rand) []string {
	t.Helper()
	h := time.Hour
	var titles []string
	n := 0
	next := func() string { n++; return fmt.Sprintf("d1v%08d", n) }
	add := func(title string, active bool, fetched, expires time.Time, expiredAt any, owner string) {
		id := next()
		seedMeta(t, db, id, title, active, fetched, expires, expiredAt)
		switch owner {
		case "video":
			seedVideo(t, db, id, storeNow.Add(-time.Duration(n%7)*h)) // repeated times: ties
		case "import":
			mustExec(t, db, `INSERT INTO title_fingerprint (video_id, fingerprint, source) VALUES (?, 'fp', 'user_import')`, id)
		}
		titles = append(titles, title)
	}
	held := func(title string) { add(title, true, storeNow.Add(-h), storeNow.Add(h), nil, "video") }

	special := []string{
		"Lofi Beats", "lofi mix", "LOFI", "Straße der Lieder", "STRASSE", "strasse",
		"Café au lait", "Cafe\u0301 nfd", "CAFÉ", "cafe", "x\u0301y", "x and x\u0301", "ab\u0301", "\u0301",
		"ﬃ office", "ΟΔΥΣΣΕΥΣ", "οδυσσευς", "İstanbul", "istanbul", "😀 emoji party 😀",
		`say "hi" now`, "100% sure", "1000", "a_b", "axb", "a*b", "aXXb", `a\b`, "ab", "a  b", "a b",
		"it's fine", "NEAR(a b)", "a OR b", "col:umn", "-minus ^caret (paren", "nul\x00byte lofi",
		"https://youtu.be/dQw4w9WgXcQ", "Now 100% sure",
	}
	for _, s := range special {
		held(s)
	}
	// Imported references with no video row.
	add("Lofi imported", true, storeNow.Add(-h), storeNow.Add(h), nil, "import")
	add("café imported", true, storeNow.Add(-h), storeNow.Add(h), nil, "import")
	// Rows the candidate filter or Held must reject on both paths.
	add("lofi expired marker", true, storeNow.Add(-h), storeNow.Add(h), storeNow.Add(-time.Minute), "video")
	add("lofi past expiry not swept", true, storeNow.Add(-3*h), storeNow.Add(-h), nil, "video")
	add("lofi inactive", false, storeNow.Add(-h), storeNow.Add(h), nil, "video")
	add("lofi future fetch", true, storeNow.Add(h), storeNow.Add(2*h), nil, "video")
	add("lofi no owner", true, storeNow.Add(-h), storeNow.Add(h), nil, "")
	add("   ", true, storeNow.Add(-h), storeNow.Add(h), nil, "video")
	add("café withheld", false, storeNow.Add(-h), storeNow.Add(h), nil, "video")
	// A row whose fetched_at is in the future but which is Indexable: it is
	// indexed, and Held at the request cutoff rejects it.
	add("beats future", true, storeNow.Add(30*time.Minute), storeNow.Add(2*h), nil, "video")

	words := []string{"lofi", "beats", "jazz", "cafe", "café", "mix", "live", "ß", "straße", "night", "a", "b", "x", "é", "😀", "100%", "_", "ﬃ"}
	for range 300 {
		var parts []string
		for range 1 + rng.IntN(5) {
			parts = append(parts, words[rng.IntN(len(words))])
		}
		held(strings.Join(parts, []string{" ", "  ", "-", ""}[rng.IntN(4)]))
	}

	_, err := metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)
	return titles
}

func d1Needles(rng *rand.Rand, titles []string) []string {
	alphabet := []rune("abcdefghijklmnopqrstuvwxyzABCÉéßﬃΣσİ ᴀ%_*\"\\'-:^(😀\u0301")
	var needles []string
	// T3 cases.
	needles = append(needles, "LOFI", "Lofi Beats", "beats", "cafe", "café", "Café", "CAFÉ", "STRASSE", "straße",
		"100%", "a_b", "a*b", `"hi"`, "it's", `a\b`, "a  b", "a b", "x\u0301", "\u0301", "xy", "ab", "ab\u0301",
		"NEAR(a", "a OR", "col:", "-minus", "^caret", "(paren", "nul\x00byte", "ΟΔΥΣΣΕΥΣ", "οδυσσευς", "ς", "σσε",
		"İst", "ist", "ﬃ", "ffi", "😀 e", "lof", "lo", "l")
	runes := func(s string) []rune { return []rune(s) }
	for len(needles) < 700 {
		switch rng.IntN(3) {
		case 0: // substring of a title
			r := runes(titles[rng.IntN(len(titles))])
			if len(r) == 0 {
				continue
			}
			a := rng.IntN(len(r))
			b := a + 1 + rng.IntN(14)
			if b > len(r) {
				b = len(r)
			}
			needles = append(needles, string(r[a:b]))
		case 1: // mutated substring
			r := runes(titles[rng.IntN(len(titles))])
			if len(r) < 3 {
				continue
			}
			a := rng.IntN(len(r) - 2)
			b := min(a+3+rng.IntN(10), len(r))
			sub := append([]rune(nil), r[a:b]...)
			sub[rng.IntN(len(sub))] = alphabet[rng.IntN(len(alphabet))]
			needles = append(needles, string(sub))
		default: // random 3–12 code points
			n := 3 + rng.IntN(10)
			r := make([]rune, n)
			for i := range r {
				r[i] = alphabet[rng.IntN(len(alphabet))]
			}
			needles = append(needles, string(r))
		}
	}
	return needles
}

func sortedMatches(ms []Match) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = fmt.Sprintf("%s|%v|%s", m.Key.VideoID, m.Key.HasDisc, m.Key.Disc.UTC().Format(time.RFC3339Nano))
	}
	slices.Sort(out)
	return out
}

// D1 at the candidate level: for ≥500 needles the indexed matches equal the
// scan matches, and the indexed candidates are a subset of the scan's.
func TestIndexedMatchesEqualScanForManyNeedles_D1(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261119, 7))
	db := openStoreDB(t)
	titles := d1Corpus(t, db, rng)
	needles := d1Needles(rng, titles)
	require.GreaterOrEqual(t, len(needles), 500)

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	ready, err := metadata.SearchIndexReady(context.Background(), tx)
	require.NoError(t, err)
	require.True(t, ready)

	now := func() time.Time { return storeNow }
	f := titlekey.NewFolder()
	var indexed, withMatch int
	for _, raw := range needles {
		n := f.Key(raw)
		scan, err := readMatches(context.Background(), tx, f, n, now, PathScan, time.Now())
		require.NoError(t, err)
		auto, err := findMatches(context.Background(), tx, f, n, now)
		require.NoError(t, err, "%q", raw)
		wantPath := PathScan
		if useIndex(true, n) {
			wantPath = PathIndexed
			indexed++
		}
		require.Equal(t, wantPath, auto.Path, "%q", raw)
		require.Equal(t, sortedMatches(scan.Matches), sortedMatches(auto.Matches), "needle %q (key %q) path %s", raw, n, auto.Path)
		require.LessOrEqual(t, auto.Candidates, scan.Candidates, "%q", raw)
		if len(scan.Matches) > 0 {
			withMatch++
		}
	}
	require.Greater(t, indexed, 400, "the corpus must exercise the indexed path")
	require.Greater(t, withMatch, 50, "the corpus must produce matches, not only empty answers")
}

// D1 end to end: the page, total and cursors from the real service agree
// between the two paths, and T10–T12 and T19 shapes hold on the indexed path.
func TestServiceIndexedEqualsScan_D1(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261119, 9))
	db := openStoreDB(t)
	titles := d1Corpus(t, db, rng)
	svc := NewService(db, func() time.Time { return storeNow })

	answer := func(q string, p Paging) (SearchResult, Path) {
		res, err := svc.Search(context.Background(), q, p)
		require.NoError(t, err, q)
		return res, res.Stats.Path
	}
	forScan := func(f func()) {
		setKeyVersion(t, db, "0/x")
		defer setKeyVersion(t, db, titlekey.Version())
		f()
	}
	same := func(q string, p Paging) (SearchResult, SearchResult) {
		a, ap := answer(q, p)
		var b SearchResult
		var bp Path
		forScan(func() { b, bp = answer(q, p) })
		require.Equal(t, PathScan, bp, q)
		if a.Kind == "results" || a.Kind == "id_not_found" {
			require.Equal(t, a.Results, b.Results, "%q on %s", q, ap)
			require.Equal(t, a.Stats.Matches, b.Stats.Matches, q)
		}
		require.Equal(t, a.Kind, b.Kind, q)
		return a, b
	}

	for _, q := range []string{"lofi", "LOFI", "cafe", "café", "straße", "STRASSE", "beats", "100%", "a_b", "a*b", `"hi"`, "ΟΔΥΣΣΕΥΣ", "😀 e", "nul\x00byte", "e\u0301x", "jazz", "zzzzzz"} {
		a, _ := same(q, Paging{})
		if a.Kind == "results" && a.Results.Total > PageSize {
			// Cursor walk: every page of the indexed path equals the scan's page.
			cursor := a.Results.NextCursor
			for page := 2; cursor != nil; page++ {
				a2, _ := same(q, Paging{Cursor: *cursor, HasCursor: true})
				require.Equal(t, page, a2.Results.Number)
				cursor = a2.Results.NextCursor
			}
			same(q, Paging{Page: "2", HasPage: true})
		}
	}

	// T12: an unknown typed ID that is a title substring → id_not_found with that
	// title in results, on the indexed path (11 code points).
	mustExec(t, db, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES ('zzzzzzzzzzz', 'see abcdefghijk now', ?, ?, 1)`, storeNow.Add(-time.Hour), storeNow.Add(time.Hour))
	mustExec(t, db, `INSERT INTO video (video_id, channel_id, discovered_at, processing_state) VALUES ('zzzzzzzzzzz', 'UC1', ?, 'discovered')`, storeNow)
	_, err := metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)
	res, path := answer("abcdefghijk", Paging{})
	require.Equal(t, PathIndexed, path)
	require.Equal(t, "id_not_found", res.Kind)
	require.Equal(t, 1, res.Results.Total)
	require.Equal(t, "zzzzzzzzzzz", res.Results.Items[0].VideoID)
	same("abcdefghijk", Paging{})

	// T19 on the indexed path: an expired row (the trigger purged it) is no
	// longer found, and the scan agrees. The title was held at the earlier
	// cutoff, so moving the marker mimics ExpireStale's UPDATE.
	res, path = answer("abcdefghijk", Paging{})
	require.Equal(t, 1, res.Results.Total)
	mustExec(t, db, `UPDATE transient_metadata SET title = NULL, expired_at = ? WHERE video_id = 'zzzzzzzzzzz'`, storeNow)
	res, path = answer("abcdefghijk", Paging{})
	require.Equal(t, PathIndexed, path)
	require.Zero(t, res.Results.Total, "trigger purge makes the entry unfindable at once")
	same("abcdefghijk", Paging{})

	// Past expires_at with no sweep: the index still proposes the row; Held
	// drops it, and the total counts only the re-checked matches.
	mustExec(t, db, `INSERT INTO transient_metadata (video_id, title, fetched_at, expires_at, is_active) VALUES ('yyyyyyyyyyy', 'unswept quokka', ?, ?, 1)`, storeNow.Add(-2*time.Hour), storeNow.Add(time.Hour))
	mustExec(t, db, `INSERT INTO video (video_id, channel_id, discovered_at, processing_state) VALUES ('yyyyyyyyyyy', 'UC1', ?, 'discovered')`, storeNow)
	_, err = metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)
	late := NewService(db, func() time.Time { return storeNow.Add(2 * time.Hour) })
	res, err = late.Search(context.Background(), "quokka", Paging{})
	require.NoError(t, err)
	require.Equal(t, PathIndexed, res.Stats.Path)
	require.Equal(t, 1, res.Stats.Candidates, "the index proposes the row")
	require.Zero(t, res.Stats.Matches, "Held at the cutoff rejects it")
	require.Zero(t, res.Results.Total)

	_ = titles
}

// The index text is a proposal only: a stale or wrong entry cannot create a
// match, because the title is re-read from transient_metadata and re-checked.
func TestIndexNeverDecides_I5(t *testing.T) {
	db := openStoreDB(t)
	seedMeta(t, db, "held-video1", "Lofi Beats", true, storeNow.Add(-time.Hour), storeNow.Add(time.Hour), nil)
	seedVideo(t, db, "held-video1", storeNow)
	_, err := metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)
	// Corrupt the stored key: the entry now proposes the row for "jazzy".
	mustExec(t, db, `UPDATE title_search SET k = 'jazzy' WHERE rowid IN (SELECT doc_id FROM title_search_doc WHERE video_id = 'held-video1')`)
	svc := NewService(db, func() time.Time { return storeNow })

	res, path := searchPath(t, svc, "jazzy")
	require.Equal(t, PathIndexed, path)
	require.Equal(t, 1, res.Stats.Candidates, "the index proposed the row")
	require.Zero(t, res.Results.Total, "the re-check against the real title rejects it")
	require.Zero(t, res.Stats.Matches)
}

func TestSearchStageTimingsAreRecordedWithoutText(t *testing.T) {
	db := openStoreDB(t)
	for i := range 20 {
		id := fmt.Sprintf("stage%06d", i)
		seedMeta(t, db, id, "Lofi Beats secretmarker", true, storeNow.Add(-time.Hour), storeNow.Add(time.Hour), nil)
		seedVideo(t, db, id, storeNow)
	}
	_, err := metadata.ReconcileSearchIndex(context.Background(), db, storeNow)
	require.NoError(t, err)
	svc := NewService(db, func() time.Time { return storeNow })
	res, path := searchPath(t, svc, "lofi")
	require.Equal(t, PathIndexed, path)
	s := res.Stats.Stages
	for name, d := range map[string]time.Duration{"begin": s.Begin, "candidates": s.Candidates, "recheck": s.Recheck, "sort": s.Sort, "project": s.Project} {
		require.Positive(t, d, name)
	}
	require.Equal(t, 20, res.Stats.Matches)
	// Stats hold only a path name, counts and durations: no field can carry
	// the search text or a title.
	require.NotContains(t, fmt.Sprintf("%+v", res.Stats), "secretmarker")
	require.NotContains(t, fmt.Sprintf("%+v", res.Stats), "lofi")
}

// The indexed path reads, never writes: T31 for the three index tables.
func TestIndexedSearchLeavesIndexTablesUnchanged_T31(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	db := openStoreDB(t)
	d1Corpus(t, db, rng)
	dump := func() string {
		var out string
		for _, tbl := range []string{"title_search_doc", "title_search_state", "title_search_content", "title_search_data", "title_search_docsize", "title_search_idx"} {
			rows, err := db.Query(`SELECT * FROM ` + tbl + ` ORDER BY 1`)
			require.NoError(t, err, tbl)
			cols, _ := rows.Columns()
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				require.NoError(t, rows.Scan(ptrs...))
				out += tbl
				for _, v := range vals {
					out += " " + toStr(v)
				}
				out += "\n"
			}
			require.NoError(t, rows.Err())
			rows.Close()
		}
		return out
	}
	before := snapshot(t, db) + dump()
	svc := NewService(db, func() time.Time { return storeNow })
	for _, q := range []string{"lofi", "café", "straße", "zzzzz", "100%"} {
		res, path := searchPath(t, svc, q)
		require.Equal(t, PathIndexed, path)
		require.NotNil(t, res.Results)
	}
	require.Equal(t, before, snapshot(t, db)+dump())
}
