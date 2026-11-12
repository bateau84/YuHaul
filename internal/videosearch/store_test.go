package videosearch

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/store"
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

	f := NewFolder()
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
	f := NewFolder()
	_, _, err = ScanMatches(ctx, tx, f, f.Key("lofi"), func() time.Time { return storeNow })
	require.Error(t, err)
}
