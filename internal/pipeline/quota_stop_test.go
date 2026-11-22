package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/discovery"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

// Regression tests for run 58 (ARS-001). The provider accepted one playlist
// insert and then answered 403 quotaExceeded to every later one. YuHaul kept
// sending and then failed the run in discovery.

const quotaExceededJSON = `{"error":{"errors":[{"reason":"quotaExceeded"}],"code":403,"message":"Forbidden"}}`

// quotaProvider is a fake YouTube. It accepts the first acceptFirst playlist
// inserts, answers 403 quotaExceeded to every later one, and records every
// other request so a test can prove none was made.
type quotaProvider struct {
	mu          sync.Mutex
	acceptFirst int
	posts       int
	other       []string
	server      *httptest.Server
}

func newQuotaProvider(t *testing.T, acceptFirst int) *quotaProvider {
	t.Helper()
	p := &quotaProvider{acceptFirst: acceptFirst}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "playlistItems") {
			p.posts++
			if p.posts <= p.acceptFirst {
				b, _ := io.ReadAll(r.Body)
				_, _ = w.Write([]byte(`{"id":"ok",` + string(b)[1:]))
				return
			}
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, quotaExceededJSON)
			return
		}
		p.other = append(p.other, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(p.server.Close)
	return p
}

func (p *quotaProvider) counts() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posts, append([]string(nil), p.other...)
}

type quotaStopRun struct {
	f      *guardedProductionFixture
	p      *Pipeline
	qs     *queue.Store
	logs   *bytes.Buffer
	runID  int64
	videos []int64
}

// newQuotaStopPipeline builds a production pipeline on the guarded fixture with
// items videos, each holding a pending quota_exhausted deferral, ready for
// StartRun to claim.
func newQuotaStopPipeline(t *testing.T, provider *quotaProvider, items int) *quotaStopRun {
	t.Helper()
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	t.Cleanup(func() { _ = f.db.Close() })

	routerStore, configStore := router.NewStore(f.db), config.NewStore(f.db)
	versioner := config.NewService(routerStore, configStore)
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)

	ids := []int64{f.videoID}
	for i := 1; i < items; i++ {
		v, err := state.NewMachine(f.state).DiscoverVideo(ctx, f.runID, fmt.Sprintf("extra-%d", i), "fixture-channel")
		require.NoError(t, err)
		seedCurrentMetadata(t, f.db, v.VideoID)
		ids = append(ids, v.ID)
	}
	for _, id := range ids {
		_, err := qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: id, RunID: f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
		require.NoError(t, err)
	}
	_, err = f.db.Exec(`UPDATE run SET status='completed', completed_at=? WHERE id=?`, time.Now().UTC(), f.runID)
	require.NoError(t, err)

	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	f.ledger = quota.New(f.db, quota.WithDefaultLimit(10_000), quota.WithLogger(logger))
	machine := state.NewMachine(f.state)
	engine := discovery.NewEngine(f.auth, machine, f.ledger, f.db, discovery.WithBaseURL(provider.server.URL), discovery.WithLogger(logger))
	machine = state.NewMachine(f.state, state.WithLedger(f.ledger))
	p := &Pipeline{
		db: f.db, stateStore: f.state, machine: machine, queueStore: qs, auth: f.auth,
		versioner: versioner, configStore: configStore, router: router.NewEngine(routerStore),
		detector: duplicate.NewDetector(f.db), discovery: engine, ledger: f.ledger,
		meta:   metadata.NewManager(metadata.NewStore(f.db), nil, nil),
		logger: logger, executor: NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.server.URL), WithExecutorLogger(logger)),
	}
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	return &quotaStopRun{f: f, p: p, qs: qs, logs: logs, videos: ids}
}

func (r *quotaStopRun) start(t *testing.T) *state.Run {
	t.Helper()
	run, err := r.p.StartRun(context.Background(), state.RunTypeScheduled)
	require.NoError(t, err)
	r.runID = run.ID
	return run
}

type runFacts struct {
	status, phase        string
	interrupt, scan      sql.NullString
	accepted, discovered int
}

func (r *quotaStopRun) runFacts(t *testing.T) runFacts {
	t.Helper()
	var f runFacts
	require.NoError(t, r.f.db.QueryRow(`SELECT status, COALESCE(current_phase,''), interrupt_reason, discovery_scan_outcome, videos_accepted, videos_discovered FROM run WHERE id=?`, r.runID).
		Scan(&f.status, &f.phase, &f.interrupt, &f.scan, &f.accepted, &f.discovered))
	return f
}

func (r *quotaStopRun) ledgerRows(t *testing.T) map[string][2]int {
	t.Helper()
	rows, err := r.f.db.Query(`SELECT operation_type||'|'||COALESCE(outcome_category,''), count(*), sum(units_consumed) FROM quota_ledger WHERE run_id=? GROUP BY 1`, r.runID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string][2]int{}
	for rows.Next() {
		var k string
		var n, u int
		require.NoError(t, rows.Scan(&k, &n, &u))
		out[k] = [2]int{n, u}
	}
	require.NoError(t, rows.Err())
	return out
}

func (r *quotaStopRun) pendingQuotaDeferrals(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, r.f.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE status='pending' AND deferral_reason='quota_exhausted'`).Scan(&n))
	return n
}

// requireNoWorkLost fails when a video has neither an outcome nor a pending row.
func (r *quotaStopRun) requirePendingOrSent(t *testing.T, sent int) {
	t.Helper()
	require.Equal(t, len(r.videos)-sent, r.pendingQuotaDeferrals(t), "every unsent video is back in the queue as pending quota_exhausted")
}

// Q1 and Q1b: the run 58 shape. One insert succeeds, the next is refused by the
// provider, and nothing further is sent, with a local limit or without one.
func TestRun58ProviderQuotaRefusalStopsTheRun(t *testing.T) {
	const items = 8
	for _, tc := range []struct {
		name  string
		limit int
	}{
		{"Q1 limit 10000", 10_000},
		{"Q1b unlimited local budget", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			provider := newQuotaProvider(t, 1)
			r := newQuotaStopPipeline(t, provider, items)
			require.NoError(t, r.f.ledger.SetDailyLimit(ctx, tc.limit))
			r.start(t)
			r.p.Execute(ctx, r.runID)

			posts, other := provider.counts()
			require.Equal(t, 2, posts, "one accepted insert and one refused insert, nothing more")
			require.Empty(t, other, "no discovery or other provider call after the refusal")

			rows := r.ledgerRows(t)
			require.Equal(t, [2]int{1, 50}, rows["playlist_items_insert|success"])
			require.Equal(t, [2]int{1, 1}, rows["playlist_items_insert|quota"], "the refusal is recorded as one unit")
			for k := range rows {
				require.NotContains(t, k, "subscriptions_list")
			}

			r.requirePendingOrSent(t, 1)
			f := r.runFacts(t)
			require.Equal(t, "completed", f.status)
			require.Equal(t, "cut_short", f.scan.String)
			require.False(t, f.interrupt.Valid)
			require.Zero(t, f.discovered)
			require.Contains(t, r.logs.String(), "youtube quota refused; run dispatch stopped")
			require.Contains(t, r.logs.String(), "discovery skipped: daily quota used up")
			require.NotContains(t, r.logs.String(), `"level":"ERROR"`)
		})
	}
}

// Q12: the ledger is empty and the provider refuses everything, as when the
// provider's day has not reset but the local day has. At most one request is sent.
func TestRun58EmptyLedgerProviderRefusesEverything(t *testing.T) {
	ctx := context.Background()
	provider := newQuotaProvider(t, 0)
	r := newQuotaStopPipeline(t, provider, 6)
	r.start(t)
	r.p.Execute(ctx, r.runID)

	posts, other := provider.counts()
	require.LessOrEqual(t, posts, 1)
	require.Empty(t, other)
	r.requirePendingOrSent(t, 0)
	f := r.runFacts(t)
	require.Equal(t, "completed", f.status)
	require.Equal(t, "cut_short", f.scan.String)
	require.False(t, f.interrupt.Valid)
	require.Equal(t, [2]int{1, 1}, r.ledgerRows(t)["playlist_items_insert|quota"])
}

// Q2: the stop is read from the ledger rows, so a new process (a fresh ledger
// and executor over the same database) still sends nothing for the run.
func TestRunQuotaStopSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixture(t)
	defer f.db.Close()
	provider := newQuotaProvider(t, 0)
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)

	send := func(e *Executor, videoID int64, externalID string, decision int64) state.ActionOutcome {
		handoff := func(tx *sql.Tx, reason queue.DeferredReason) error {
			_, err := qs.CreateDeferredItemTx(ctx, tx, &queue.DeferredItem{VideoID: videoID, RunID: f.runID, Reason: reason, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
			return err
		}
		scoped := quota.WithRun(withGuardedDeferral(ctx, handoff), f.runID)
		outcome, err := e.executeGuardedAction(scoped, f.runID, videoID, externalID, action{RouteDecisionID: decision, ActionType: "playlist_insert", TargetPlaylistID: f.target}, &f.binding)
		require.NoError(t, err)
		return outcome
	}

	first := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.server.URL))
	require.Equal(t, state.ActionOutcomeDeferred, send(first, f.videoID, "guarded-external-video", f.decisionID))
	posts, _ := provider.counts()
	require.Equal(t, 1, posts)

	// Restart: new ledger and executor, same database and run.
	ledger := quota.New(f.db, quota.WithDefaultLimit(10_000))
	second := NewExecutor(f.db, ledger, f.auth, WithYouTubeBaseURL(provider.server.URL))
	next, err := state.NewMachine(f.state).DiscoverVideo(ctx, f.runID, "after-restart", "fixture-channel")
	require.NoError(t, err)
	decision := createRouteDecision(t, ctx, f.state, next.ID, f.runID, f.routeID, 1, state.RouteDecisionMatched)
	require.Equal(t, state.ActionOutcomeDeferred, send(second, next.ID, "after-restart", decision))
	posts, _ = provider.counts()
	require.Equal(t, 1, posts, "the resumed run sent nothing")

	ok, _, err := ledger.CanAfford(quota.WithRun(ctx, f.runID), 1)
	require.NoError(t, err)
	require.False(t, ok)
	var pending int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE status='pending' AND deferral_reason='quota_exhausted'`).Scan(&pending))
	require.Equal(t, 2, pending)
}

// Q11: when quota is known to be used up, StartRun creates the run and claims
// nothing. The deferred rows stay exactly as they were.
func TestStartRunClaimsNothingWhenQuotaIsKnownUsedUp(t *testing.T) {
	ctx := context.Background()
	provider := newQuotaProvider(t, 1)
	r := newQuotaStopPipeline(t, provider, 3)
	require.NoError(t, r.f.ledger.SetDailyLimit(ctx, 1))
	require.NoError(t, r.f.ledger.Record(ctx, sql.NullInt64{}, quota.OpVideosList))

	snapshot := func() string {
		rows, err := r.f.db.Query(`SELECT * FROM deferred_item ORDER BY id`)
		require.NoError(t, err)
		defer rows.Close()
		cols, err := rows.Columns()
		require.NoError(t, err)
		var out strings.Builder
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(ptrs...))
			fmt.Fprintf(&out, "%v\n", vals)
		}
		require.NoError(t, rows.Err())
		return out.String()
	}
	before := snapshot()
	run := r.start(t)
	require.NotZero(t, run.ID)
	require.Equal(t, before, snapshot(), "deferred rows are unchanged byte for byte")
	var owned int
	require.NoError(t, r.f.db.QueryRow(`SELECT COUNT(*) FROM deferred_item WHERE retry_run_id IS NOT NULL`).Scan(&owned))
	require.Zero(t, owned)
	posts, other := provider.counts()
	require.Zero(t, posts)
	require.Empty(t, other)
}
