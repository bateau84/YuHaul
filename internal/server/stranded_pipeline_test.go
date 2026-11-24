package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// The stranded-video routes over the real pipeline, migrated SQLite and the
// real queue service: the wire answers come from the pipeline's own types and
// the writes are the pipeline's one transaction. No YouTube client exists in
// this composition, so a provider call could not happen.

type strandedEnv struct {
	db  *sql.DB
	srv *Server
	pl  *pipeline.Pipeline
}

func newStrandedEnv(t *testing.T, openGate bool) *strandedEnv {
	t.Helper()
	db, stateStore, machine, cleanup := setupServerTest(t)
	t.Cleanup(cleanup)
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	pl := pipeline.NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, pipeline.NewBroadcaster())
	if openGate {
		pl.OpenProducers()
	} else {
		// The server's own startup step: it closes the gate, and nothing
		// opens it again until recovery has resumed its runs.
		_, err := pl.BeginStartupRecovery(context.Background())
		require.NoError(t, err)
	}
	srv := NewServer(pl, pipeline.NewBroadcaster(),
		WithServerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithQueueHandler(queue.NewService(qs, nil)))
	return &strandedEnv{db: db, srv: srv, pl: pl}
}

// seedStranded adds an ended (failed) run and n unfinished videos it
// discovered: each is stranded.
func (e *strandedEnv) seedStranded(t *testing.T, n int) {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO run (started_at, completed_at, run_type, status) VALUES (?, ?, 'manual', 'failed')`,
		time.Now().UTC().Add(-3*time.Hour), time.Now().UTC().Add(-2*time.Hour))
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)
	for i := 0; i < n; i++ {
		_, err := e.db.Exec(`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?, 'UC1', ?, ?, 'not_yet_evaluated')`,
			"sp"+string(rune('a'+i)), runID, state.VideoStateNotYetEvaluated)
		require.NoError(t, err)
	}
}

func (e *strandedEnv) items(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, e.db.QueryRow(`SELECT count(*) FROM deferred_item`).Scan(&n))
	return n
}

func decode(t *testing.T, rec interface{ Bytes() []byte }) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(rec.Bytes(), &m))
	return m
}

func TestStrandedRoutesOverTheRealPipeline(t *testing.T) {
	e := newStrandedEnv(t, true)
	e.seedStranded(t, 3)

	rec := do(e.srv, http.MethodGet, "/api/stranded-videos", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	p := decode(t, rec.Body)
	require.Equal(t, true, p["available"])
	require.Nil(t, p["unavailable_reason"])
	require.EqualValues(t, 3, p["requeue"])
	require.Zero(t, e.items(t), "the preview writes nothing")

	rec = do(e.srv, http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":3}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	r := decode(t, rec.Body)
	require.Equal(t, "requeued", r["outcome"])
	require.EqualValues(t, 3, r["requeued"])
	require.EqualValues(t, 0, r["skipped_concurrently"])
	require.EqualValues(t, 3, r["expected_requeue"])
	require.Equal(t, false, r["differs_from_preview"])
	require.Equal(t, 3, e.items(t))
	var pending int
	require.NoError(t, e.db.QueryRow(`SELECT count(*) FROM deferred_item WHERE status='pending' AND deferral_reason='requeued_by_user' AND retry_after IS NULL`).Scan(&pending))
	require.Equal(t, 3, pending)

	// Repeating changes nothing and says so; the preview now counts none.
	rec = do(e.srv, http.MethodPost, "/api/stranded-videos/requeue", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	r = decode(t, rec.Body)
	require.Equal(t, "nothing_to_requeue", r["outcome"])
	require.EqualValues(t, 0, r["requeued"])
	require.Nil(t, r["expected_requeue"])
	require.Equal(t, 3, e.items(t))
	p = decode(t, do(e.srv, http.MethodGet, "/api/stranded-videos", "").Body)
	require.EqualValues(t, 0, p["requeue"])
	require.EqualValues(t, 3, p["already_queued"].(map[string]any)["videos"])

	// The paged Deferred read, composed in the same server, shows them.
	d := decode(t, do(e.srv, http.MethodGet, "/api/deferred", "").Body)
	require.EqualValues(t, 3, d["total"])
	require.Equal(t, map[string]any{"requeued_by_user": float64(3)}, d["by_reason"])
}

func TestStrandedRoutesRefuseDuringStartupRecoveryAndShutdown(t *testing.T) {
	t.Run("startup recovery", func(t *testing.T) {
		e := newStrandedEnv(t, false)
		e.seedStranded(t, 2)
		p := decode(t, do(e.srv, http.MethodGet, "/api/stranded-videos", "").Body)
		require.Equal(t, false, p["available"])
		require.Equal(t, "startup_recovery_active", p["unavailable_reason"])
		require.EqualValues(t, 2, p["requeue"], "the counts stay current")

		rec := do(e.srv, http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":2}`)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, "startup_recovery_active", decode(t, rec.Body)["code"])
		require.Zero(t, e.items(t))
		require.NoError(t, e.srv.WaitRuns(shortCtx(t)))
	})
	t.Run("shutdown", func(t *testing.T) {
		e := newStrandedEnv(t, true)
		e.seedStranded(t, 2)
		require.NoError(t, e.srv.WaitRuns(context.Background()))

		rec := do(e.srv, http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":2}`)
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.JSONEq(t, `{"error":"server is shutting down","code":"shutting_down"}`, rec.Body.String())
		require.Zero(t, e.items(t), "a refused confirm changes nothing")

		p := decode(t, do(e.srv, http.MethodGet, "/api/stranded-videos", "").Body)
		require.Equal(t, false, p["available"])
		require.Equal(t, "shutting_down", p["unavailable_reason"])
		require.EqualValues(t, 2, p["requeue"])
	})
}
