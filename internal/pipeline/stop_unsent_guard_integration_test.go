//go:build integration

package pipeline

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"modernc.org/sqlite"

	"github.com/bateau84/YuHaul/internal/store"
)

// admissionBarrier holds the first playlist dispatch between its guard claim
// and its quota admission, the same driver-level hook pattern as
// internal/auth/dispatch_quota_test.go. The claim is the INSERT into
// playlist_send_guard; admission begins with the next quota_settings read
// (quota.Ledger.Acquire). The read blocks until its context ends, so the
// release is the user's stop reaching the execution context.
type admissionBarrier struct {
	drv     driver.Driver
	dsn     string
	claimed atomic.Bool
	fired   atomic.Bool
	entered chan struct{}
}

func newAdmissionBarrier() *admissionBarrier {
	return &admissionBarrier{drv: &sqlite.Driver{}, entered: make(chan struct{})}
}

// open is a database opener for setupIdempotencyTestOpen. It mirrors
// store.Open (same DSN options, migrations and backfill) over the barrier.
func (b *admissionBarrier) open(ctx context.Context, path string) (*sql.DB, error) {
	b.dsn = path + "?_fk=1&_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1"
	db := sql.OpenDB(b)
	if err := store.ApplyMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.BackfillRouteIdentities(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (b *admissionBarrier) Connect(context.Context) (driver.Conn, error) {
	conn, err := b.drv.Open(b.dsn)
	if err != nil {
		return nil, err
	}
	return &admissionBarrierConn{Conn: conn, barrier: b}, nil
}

func (b *admissionBarrier) Driver() driver.Driver { return b.drv }

type admissionBarrierConn struct {
	driver.Conn
	barrier *admissionBarrier
}

func (c *admissionBarrierConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.Contains(query, "INSERT INTO playlist_send_guard") {
		c.barrier.claimed.Store(true)
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *admissionBarrierConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "INSERT INTO playlist_send_guard") {
		c.barrier.claimed.Store(true)
	}
	if strings.Contains(query, "quota_settings") && c.barrier.claimed.Load() && c.barrier.fired.CompareAndSwap(false, true) {
		close(c.barrier.entered)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

// S3, unsent branch (AC7–8): a stop that arrives after the guard claim and
// before admission sends nothing. The guard is released, the action is
// not_attempted without an attempt time, the video is handed over, and the next
// run sends it exactly once.
func TestS3_StopBetweenGuardClaimAndAdmissionReleasesTheGuard(t *testing.T) {
	barrier := newAdmissionBarrier()
	e := newRunNowEnvOpen(t, barrier.open)

	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	done := startExecution(func() { adm.Execute(e.ctx) })
	select {
	case <-barrier.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the dispatch never reached admission")
	}
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM playlist_send_guard`), "the guard is claimed while admission is held")
	require.Zero(t, e.totalPosts())

	e.stop(t, adm.RunID(), StopAccepted)
	waitDone(t, done)
	e.assertStopped(t, adm.RunID())

	require.Zero(t, e.totalPosts(), "nothing reached the provider")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM playlist_send_guard`), "the guard is released")
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM executed_action WHERE run_id=? AND outcome='not_attempted' AND executed_at IS NULL`, adm.RunID()),
		"the unsent action is not_attempted with no attempt time")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM executed_action WHERE run_id=? AND outcome<>'not_attempted'`, adm.RunID()))
	require.Equal(t, map[string]int{"V1": 1, "V2": 1, "V3": 1}, e.pendingHandoffs(t, "stopped_by_user"), "every unfinished video, including the unsent one, is handed over")
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM error_entry`))

	next, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	require.False(t, next.Resumed())
	next.Execute(e.ctx)
	require.Equal(t, "completed", string(e.run(t, next.RunID()).Status))
	for _, v := range []string{"V1", "V2", "V3"} {
		require.Equal(t, 1, e.postsFor(v), "%s is sent exactly once", v)
	}
}
