package stats

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

type trendRun struct {
	runType, status string
	scanOutcome     sql.NullString // NULL = legacy run with no saved outcome
	phase           string
	discovered      int
	accepted        int
	rejected        int
	deferred        int
	failed          int
}

func seedTrendRun(t *testing.T, ctx context.Context, db *sql.DB, at time.Time, r trendRun) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx,
		`INSERT INTO run (started_at, run_type, status, discovery_scan_outcome, current_phase,
		                  videos_discovered, videos_accepted, videos_rejected, videos_deferred, videos_failed)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		at.UTC().Format(state.RunTimestampLayout), r.runType, insertStatus(r.status), r.scanOutcome,
		sql.NullString{String: r.phase, Valid: r.phase != ""},
		r.discovered, r.accepted, r.rejected, r.deferred, r.failed)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	if r.status == "stopped" {
		// Triggers forbid inserting a stopped run: follow the real path
		// (running -> stop requested -> stopped).
		_, err = db.ExecContext(ctx, `UPDATE run SET stop_requested_at = ? WHERE id = ?;`, at.UTC().Format(state.RunTimestampLayout), id)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `UPDATE run SET status = 'stopped' WHERE id = ?;`, id)
		require.NoError(t, err)
	}
	return id
}

func insertStatus(status string) string {
	if status == "stopped" {
		return "running"
	}
	return status
}

func outcome(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

// TestGetTemporalStats_RunInclusion seeds one run per type/status combination
// plus the discovery-finished cases and checks BR-023 AC10–AC13.
func TestGetTemporalStats_RunInclusion(t *testing.T) {
	ctx := context.Background()
	db, s, _, cleanup := setupStatsTest(t)
	defer cleanup()

	day := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	runs := []trendRun{
		// Counted, discovery finished (legacy: NULL outcome + completed).
		{runType: "manual", status: "completed", discovered: 100, accepted: 10, rejected: 1, deferred: 1, failed: 1},
		// Counted, saved outcome completed, then failed in routing: discovered counts.
		{runType: "scheduled", status: "failed", scanOutcome: outcome("completed"), phase: "routing", discovered: 50, accepted: 5, rejected: 2},
		// Counted, saved cut_short: outcome counts yes, discovered no (even though status completed).
		{runType: "scheduled", status: "completed", scanOutcome: outcome("cut_short"), phase: "routing", discovered: 500, accepted: 3, failed: 3},
		// Counted, saved no_source_scanned: discovered no.
		{runType: "scheduled", status: "failed", scanOutcome: outcome("no_source_scanned"), discovered: 400, failed: 4},
		// Legacy failed run at action_execution, NULL outcome: discovered excluded, accepted still counted.
		{runType: "scheduled", status: "failed", phase: "action_execution", discovered: 2000, accepted: 7, deferred: 2},
		// Stopped manual legacy run: not completed -> discovered excluded, outcomes counted.
		{runType: "manual", status: "stopped", phase: "routing", discovered: 300, accepted: 1, rejected: 1},
		// Never counted: non-terminal statuses.
		{runType: "manual", status: "running", discovered: 9000, accepted: 900},
		{runType: "scheduled", status: "interrupted", discovered: 9000, accepted: 900},
		// Never counted: non-production types.
		{runType: "dry_run", status: "completed", discovered: 9000, accepted: 900},
		{runType: "replay", status: "completed", discovered: 9000, accepted: 900},
		{runType: "backtest", status: "completed", discovered: 9000, accepted: 900},
	}
	for i, r := range runs {
		seedTrendRun(t, ctx, db, day.Add(time.Duration(i)*time.Minute), r)
	}

	buckets, err := s.GetTemporalStats(ctx, day.Add(-time.Hour), day.Add(time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	b := buckets[0]

	// Outcome counts: runs 1-6 only.
	assert.Equal(t, 10+5+3+0+7+1, b.VideosAccepted)
	assert.Equal(t, 1+2+0+0+0+1, b.VideosRejected)
	assert.Equal(t, 1+2, b.VideosDeferred)
	assert.Equal(t, 1+3+4, b.VideosFailed)
	// Discovered: only runs 1 and 2.
	assert.Equal(t, 150, b.VideosDiscovered)
	// Rate uses accepted from the same two runs: 15/150.
	require.NotNil(t, b.AcceptanceRate)
	assert.InDelta(t, 0.10, *b.AcceptanceRate, 1e-9)
}

func TestGetTemporalStats_NoDataIsNotZeroPercent(t *testing.T) {
	ctx := context.Background()
	db, s, _, cleanup := setupStatsTest(t)
	defer cleanup()

	day := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	// Only a failed legacy run at action_execution: counted outcomes, no finished discovery.
	seedTrendRun(t, ctx, db, day, trendRun{runType: "scheduled", status: "failed", phase: "action_execution", discovered: 2000, accepted: 7})

	buckets, err := s.GetTemporalStats(ctx, day.Add(-time.Hour), day.Add(time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, buckets, 1)
	assert.Equal(t, 7, buckets[0].VideosAccepted, "accepted from the failed run still counts")
	assert.Equal(t, 0, buckets[0].VideosDiscovered, "discovered from a run whose discovery did not finish is excluded")
	assert.Nil(t, buckets[0].AcceptanceRate, "no finished-discovery run means no data, not 0%")

	raw, err := json.Marshal(buckets[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"acceptance_rate":null`)
}

func TestGetRouteMatchRates_RunInclusion(t *testing.T) {
	ctx := context.Background()
	db, s, _, cleanup := setupStatsTest(t)
	defer cleanup()

	day := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	routeID := seedRoute(t, ctx, db, "Music", 1)

	cases := []struct {
		runType, status string
		counted         bool
	}{
		{"scheduled", "failed", true},
		{"manual", "stopped", true},
		{"manual", "completed", true},
		{"scheduled", "interrupted", false},
		{"manual", "running", false},
		{"dry_run", "completed", false},
		{"replay", "completed", false},
		{"backtest", "completed", false},
	}
	want := 0
	for i, c := range cases {
		runID := seedTrendRun(t, ctx, db, day.Add(time.Duration(i)*time.Minute), trendRun{runType: c.runType, status: c.status})
		res, err := db.ExecContext(ctx,
			`INSERT INTO video (video_id, channel_id, discovered_at, discovered_in_run_id, processing_state, final_outcome)
			 VALUES (?, 'UC1', ?, ?, 'completed', 'accepted');`, "v"+c.runType+c.status, day.Format(state.RunTimestampLayout), runID)
		require.NoError(t, err)
		vid, err := res.LastInsertId()
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`INSERT INTO route_decision (video_id, run_id, route_id, evaluation_order, decision)
			 VALUES (?, ?, ?, 1, 'matched');`, vid, runID, routeID)
		require.NoError(t, err)
		if c.counted {
			want++
		}
	}

	rates, err := s.GetRouteMatchRates(ctx, day.Add(-time.Hour), day.Add(time.Hour), "day")
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.Equal(t, want, rates[0].Evaluated)
}

// TestHandler_Trends_WebContract decodes the real HTTP response into the keys
// the web TrendBucket type consumes (web/src/types.ts). Renaming a JSON key
// here without the web side must fail this test.
func TestHandler_Trends_WebContract(t *testing.T) {
	ctx := context.Background()
	db, _, service, cleanup := setupStatsTest(t)
	defer cleanup()

	day := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	seedTrendRun(t, ctx, db, day, trendRun{runType: "manual", status: "completed", discovered: 40, accepted: 8, rejected: 20, deferred: 6, failed: 2})

	handler := NewHandler(service, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	q := url.Values{"from": {day.Add(-time.Hour).Format(time.RFC3339)}, "to": {day.Add(time.Hour).Format(time.RFC3339)}}
	req := httptest.NewRequest(http.MethodGet, "/api/analytics/trends?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var payload struct {
		Buckets []struct {
			Bucket                string   `json:"bucket"`
			VideosDiscovered      *int     `json:"videos_discovered"`
			VideosAccepted        *int     `json:"videos_accepted"`
			VideosRejected        *int     `json:"videos_rejected"`
			VideosDeferred        *int     `json:"videos_deferred"`
			VideosFailed          *int     `json:"videos_failed"`
			VideosNotYetEvaluated *int     `json:"videos_not_yet_evaluated"`
			VideosUnavailable     *int     `json:"videos_unavailable"`
			AcceptanceRate        *float64 `json:"acceptance_rate"`
		} `json:"buckets"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Buckets, 1)
	b := payload.Buckets[0]
	for name, p := range map[string]*int{
		"videos_discovered": b.VideosDiscovered, "videos_accepted": b.VideosAccepted,
		"videos_rejected": b.VideosRejected, "videos_deferred": b.VideosDeferred,
		"videos_failed": b.VideosFailed, "videos_not_yet_evaluated": b.VideosNotYetEvaluated,
		"videos_unavailable": b.VideosUnavailable,
	} {
		require.NotNil(t, p, "key %s missing from the trends JSON", name)
	}
	assert.Equal(t, 40, *b.VideosDiscovered)
	assert.Equal(t, 8, *b.VideosAccepted)
	require.NotNil(t, b.AcceptanceRate)
	assert.InDelta(t, 0.2, *b.AcceptanceRate, 1e-9)
}
