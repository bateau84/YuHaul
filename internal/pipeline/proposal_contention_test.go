package pipeline

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"
)

// The lookup probe attempts a competing commit after the proposal has started
// reading its identity snapshot, but before its first business-data write.
// A deferred reader loses its snapshot; a reserved writer safely excludes it.
func TestProposalIngestReservesWriterBeforeIdentityRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var compete func() error
	var competingErr error
	probed := false
	name := "compete_" + uuid.New().String()[:8]
	require.NoError(t, sqlite.RegisterScalarFunction(name, 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		probed = true
		competingErr = compete()
		return args[0], nil
	}))
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "proposal.db"))
	require.NoError(t, err)
	defer db.Close()
	s, err := state.NewStore(db)
	require.NoError(t, err)
	p := &Pipeline{db: db, stateStore: s, configStore: config.NewStore(db)}
	baseline := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning}
	baseline.ID, err = s.CreateRun(ctx, baseline)
	require.NoError(t, err)
	proposal := proposalSnapshot(proposalRoute(proposalTestUUID, "proposal", "jazz"))
	newRun := func() *state.Run {
		return &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
	}
	require.NoError(t, p.ingestProposalRun(ctx, "seed identity", proposal, newRun()))
	// A transparent read view lets the actual lookup invoke our interleaving
	// probe. All route keys and identity rows are real, persisted ingest output.
	_, err = db.ExecContext(ctx, `ALTER TABLE route_identity RENAME TO identity_rows;
		CREATE VIEW route_identity AS SELECT route_id, `+name+`(public_id) AS public_id FROM identity_rows;`)
	require.NoError(t, err)
	competitor, err := db.Conn(ctx)
	require.NoError(t, err)
	defer competitor.Close()
	_, err = competitor.ExecContext(ctx, `PRAGMA busy_timeout = 0`)
	require.NoError(t, err)
	compete = func() error {
		_, err := competitor.ExecContext(ctx, `UPDATE run SET quota_cost = quota_cost + 1 WHERE id = ?`, baseline.ID)
		return err
	}
	run := newRun()
	require.NoError(t, p.tryIngestProposalRun(ctx, "contended proposal", proposal, run))
	require.True(t, probed, "the proposal must exercise the real identity lookup")
	require.Error(t, competingErr, "the proposal must hold the writer reservation while reading")
	var sqliteErr *sqlite.Error
	require.ErrorAs(t, competingErr, &sqliteErr)
	require.Equal(t, 5, sqliteErr.Code()&0xff, "competing writer must get SQLITE_BUSY")
	stored, err := s.GetRun(ctx, run.ID)
	require.NoError(t, err)
	cv, err := p.configStore.Get(ctx, *stored.ConfigVersionID)
	require.NoError(t, err)
	require.Len(t, cv.Snapshot.Routes, 1)
	var mapped int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT route_id FROM identity_rows WHERE public_id = ?`, proposalTestUUID).Scan(&mapped))
	require.Equal(t, mapped, cv.Snapshot.Routes[0].ID)
}

func TestProposalIngestConcurrentWritersCommitAtomically(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "concurrent.db"))
	require.NoError(t, err)
	defer db.Close()
	s, err := state.NewStore(db)
	require.NoError(t, err)
	p := &Pipeline{db: db, stateStore: s, configStore: config.NewStore(db)}
	const workers = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	runs := make([]state.Run, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Go(func() {
			<-start
			runs[i] = state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
			errs[i] = p.ingestProposalRun(ctx, "concurrent", proposalSnapshot(proposalRoute(proposalTestUUID, "proposal", "jazz")), &runs[i])
		})
	}
	close(start)
	wg.Wait()
	var mapped int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT route_id FROM route_identity WHERE public_id = ?`, proposalTestUUID).Scan(&mapped))
	for i := range workers {
		require.NoError(t, errs[i], "worker %d", i)
		stored, err := s.GetRun(ctx, runs[i].ID)
		require.NoError(t, err)
		cv, err := p.configStore.Get(ctx, *stored.ConfigVersionID)
		require.NoError(t, err)
		require.Len(t, cv.Snapshot.Routes, 1)
		require.Equal(t, mapped, cv.Snapshot.Routes[0].ID)
	}
	for _, check := range []struct {
		query string
		want  int
	}{
		{`SELECT COUNT(*) FROM run`, workers},
		{`SELECT COUNT(*) FROM config_version`, workers},
		{`SELECT COUNT(*) FROM route_identity`, 1},
		{`SELECT COUNT(*) FROM route WHERE deleted_at IS NOT NULL AND is_enabled = 0`, 1},
		{`SELECT COUNT(*) FROM route WHERE deleted_at IS NULL`, 0},
		{`SELECT COUNT(*) FROM pragma_foreign_key_check`, 0},
	} {
		var got int
		require.NoError(t, db.QueryRowContext(ctx, check.query).Scan(&got))
		require.Equal(t, check.want, got, check.query)
	}
}

func TestProposalIngestWriterContentionLeavesNoPartialRecords(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "busy.db")
	db, err := store.Open(ctx, path)
	require.NoError(t, err)
	defer db.Close()
	writer, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer writer.Rollback()
	_, err = writer.ExecContext(ctx, `UPDATE run SET id = id WHERE 0`)
	require.NoError(t, err)
	// A zero busy timeout deterministically exercises acquisition failure,
	// rather than relying on wall-clock contention duration.
	ingestDB, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(0)")
	require.NoError(t, err)
	defer ingestDB.Close()
	s, err := state.NewStore(ingestDB)
	require.NoError(t, err)
	p := &Pipeline{db: ingestDB, stateStore: s, configStore: config.NewStore(ingestDB)}
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeBacktest, Status: state.RunStatusRunning}
	err = p.ingestProposalRun(ctx, "blocked", proposalSnapshot(proposalRoute(proposalTestUUID, "proposal", "jazz")), run)
	require.ErrorContains(t, err, "reserve proposal writer")
	require.Zero(t, run.ID)
	require.Nil(t, run.ConfigVersionID)
	require.NoError(t, writer.Rollback())
	for _, table := range []string{"route", "route_identity", "config_version", "run"} {
		var n int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n))
		require.Zero(t, n, table)
	}
	// The connection/transaction remains usable after the failed reservation.
	require.NoError(t, p.ingestProposalRun(ctx, "unblocked", proposalSnapshot(proposalRoute(proposalTestUUID, "proposal", "jazz")), run))
}
