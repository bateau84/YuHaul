package state

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func srcChan(c byte) string { return "UC" + strings.Repeat(string(c), 22) }

func recordFailure(t *testing.T, s *Store, runID int64, channel string, at time.Time) {
	t.Helper()
	tx, err := s.db.Begin()
	require.NoError(t, err)
	require.NoError(t, RecordUploadsSourceFailureTx(context.Background(), tx, runID, channel, at))
	require.NoError(t, tx.Commit())
}

func TestRecordUploadsSourceFailureIsIdempotentPerRunAndChannel(t *testing.T) {
	s := newStore(t)
	run, err := insertRunSQL(t, s, "manual", "running", nil, nil)
	require.NoError(t, err)
	first := time.Date(2026, 11, 20, 1, 2, 3, 0, time.UTC)
	recordFailure(t, s, run, srcChan('B'), first)
	recordFailure(t, s, run, srcChan('B'), first.Add(time.Hour)) // no-op, the earlier time stands

	page, err := s.ListSourceFailures(context.Background(), SourceFailureQuery{RunID: run, Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "2026-11-20T01:02:03Z", *page.Items[0].OccurredAt)
	require.Equal(t, "playlistNotFound", *page.Items[0].Reason)
	require.Equal(t, 404, *page.Items[0].HTTPStatus)

	tx, err := s.db.Begin()
	require.NoError(t, err)
	defer tx.Rollback()
	require.Error(t, RecordUploadsSourceFailureTx(context.Background(), tx, run, "UCshort", first), "an invalid channel ID is refused before SQL")
}

func TestPermanentSourceFailureChannelsIsPerRun(t *testing.T) {
	s := newStore(t)
	r1, _ := insertRunSQL(t, s, "manual", "running", nil, nil)
	r2, _ := insertRunSQL(t, s, "manual", "running", nil, nil)
	recordFailure(t, s, r1, srcChan('B'), time.Now())
	recordFailure(t, s, r2, srcChan('C'), time.Now())
	got, err := PermanentSourceFailureChannels(context.Background(), s.db, r1)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{srcChan('B'): true}, got)
}

func TestListSourceFailuresPagesNewestFirstWithJoinedName(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	run, _ := insertRunSQL(t, s, "manual", "completed", nil, nil)
	other, _ := insertRunSQL(t, s, "scheduled", "completed", nil, nil)
	for _, c := range []byte{'A', 'B', 'C'} {
		recordFailure(t, s, run, srcChan(c), time.Now())
	}
	recordFailure(t, s, other, srcChan('A'), time.Now())
	_, err := s.db.Exec(`INSERT INTO channel (channel_id, channel_name, first_discovered_at, last_discovered_at, total_discovered) VALUES (?, 'Channel B', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z', 1)`, srcChan('B'))
	require.NoError(t, err)

	all, err := s.ListSourceFailures(ctx, SourceFailureQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, all.Items, 4)
	require.Nil(t, all.NextBeforeID)
	for i := 1; i < len(all.Items); i++ {
		require.Greater(t, all.Items[i-1].ID, all.Items[i].ID, "newest first")
	}

	p1, err := s.ListSourceFailures(ctx, SourceFailureQuery{Limit: 3})
	require.NoError(t, err)
	require.Len(t, p1.Items, 3)
	require.NotNil(t, p1.NextBeforeID)
	require.Equal(t, p1.Items[2].ID, *p1.NextBeforeID)
	p2, err := s.ListSourceFailures(ctx, SourceFailureQuery{Limit: 3, BeforeID: *p1.NextBeforeID})
	require.NoError(t, err)
	require.Len(t, p2.Items, 1)
	require.Nil(t, p2.NextBeforeID)

	scoped, err := s.ListSourceFailures(ctx, SourceFailureQuery{RunID: run, Limit: 10})
	require.NoError(t, err)
	require.Len(t, scoped.Items, 3)
	for _, f := range scoped.Items {
		require.Equal(t, run, f.RunID)
		if *f.ChannelID == srcChan('B') {
			require.Equal(t, "Channel B", *f.ChannelName)
		} else {
			require.Nil(t, f.ChannelName, "no channel row, no name")
		}
	}
	none, err := s.ListSourceFailures(ctx, SourceFailureQuery{RunID: 9999, Limit: 10})
	require.NoError(t, err)
	require.NotNil(t, none.Items, "an empty page is an empty list, not null")
	require.Empty(t, none.Items)
}

func TestListSourceFailuresWithholdsWhatItCannotRelease(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	run, _ := insertRunSQL(t, s, "manual", "completed", nil, nil)
	recordFailure(t, s, run, srcChan('A'), time.Now())
	// Rows the CHECKs would normally refuse: injected with checks off.
	// The test database is one in-memory connection; run these statements
	// sequentially on it.
	s.db.SetMaxOpenConns(1)
	_, err := s.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`)
	require.NoError(t, err)
	ins := func(channel, category, op string, reason any) {
		_, err := s.db.ExecContext(ctx, `INSERT INTO discovery_source_failure (run_id, channel_id, occurred_at, category, operation, http_status, reason) VALUES (?, ?, 'not a time', ?, ?, 404, ?)`, run, channel, category, op, reason)
		require.NoError(t, err)
	}
	ins("not-a-channel-id-xxxxxxx", "transient", "playlist_items_list", "error: https://secret/playlist?key=1") // invalid id, unvetted reason, bad time
	ins(srcChan('C'), "mystery", "playlist_items_list", "playlistNotFound")                                     // unknown category
	ins(srcChan('D'), "permanent", "videos_list", "playlistNotFound")                                           // unknown operation
	_, err = s.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = OFF`)
	require.NoError(t, err)

	page, err := s.ListSourceFailures(ctx, SourceFailureQuery{RunID: run, Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 2, page.Withheld)
	require.Len(t, page.Items, 2)
	odd := page.Items[0]
	require.Nil(t, odd.ChannelID, "an invalid channel ID is nulled")
	require.Nil(t, odd.Reason, "an unvetted reason is nulled")
	require.Nil(t, odd.OccurredAt, "an unreadable time is nulled")
	require.Equal(t, "transient", odd.Category)
}

func TestListSourceFailuresPermanentOnlyAndStorageError(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	run, _ := insertRunSQL(t, s, "manual", "completed", nil, nil)
	recordFailure(t, s, run, srcChan('A'), time.Now())
	_, err := s.db.Exec(`INSERT INTO discovery_source_failure (run_id, channel_id, occurred_at, category, operation, http_status, reason) VALUES (?, ?, '2026-11-20T00:00:00Z', 'transient', 'playlist_items_list', 500, 'backendError')`, run, srcChan('T'))
	require.NoError(t, err)

	perm, err := s.ListSourceFailures(ctx, SourceFailureQuery{PermanentOnly: true, Limit: 10})
	require.NoError(t, err)
	require.Len(t, perm.Items, 1)
	runScoped, err := s.ListSourceFailures(ctx, SourceFailureQuery{RunID: run, Limit: 10})
	require.NoError(t, err)
	require.Len(t, runScoped.Items, 2, "the run list carries every category")

	_, err = s.ListSourceFailures(ctx, SourceFailureQuery{Limit: 0})
	require.Error(t, err)
	var _ sql.DB // keep database/sql imported for the helper above
	require.NoError(t, s.db.Close())
	_, err = s.ListSourceFailures(ctx, SourceFailureQuery{Limit: 10})
	require.Error(t, err, "a storage failure is never an empty page")
}
