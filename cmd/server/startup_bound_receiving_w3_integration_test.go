//go:build integration

package main

// Assessment 359587ee, finding ee32de34, through the real server composition
// (ARS-005 section 6.1.3, writers W1 and W3). A registered Run Now claims two
// due deferred items. The account is connected and its access token is valid
// locally (no refresh credential), so Bind succeeds; the loopback provider
// answers the receiving videos.list with 401 authError, which makes W1 settle
// the pass as authorization_pending. When the database refuses only the write
// of dispatch_stop='authorization', the run must end through W3: the integrity
// stop is stored, the registered SQLite reason is the run's durable reason (DB
// and API), no further provider request is made, and the exact work stays
// queued. The same stimulus without the fault is the control.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

// authErrorProvider answers every videos.list with 401 authError and counts what
// it receives.
type authErrorProvider struct {
	srv   *httptest.Server
	mu    sync.Mutex
	paths []string
}

func newAuthErrorProvider(t *testing.T) *authErrorProvider {
	t.Helper()
	p := &authErrorProvider{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.paths = append(p.paths, r.Method+" "+r.URL.Path)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/videos") {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":401,"message":"Invalid Credentials","errors":[{"reason":"authError","message":"Invalid Credentials"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *authErrorProvider) videosListCount() (videos, other int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, path := range p.paths {
		if strings.HasPrefix(path, "GET /videos") {
			videos++
		} else {
			other++
		}
	}
	return
}

type dueSeed struct {
	items [2]int64
	vids  [2]int64
	age   string
}

// seedDueItems leaves a stored connected account with a locally valid access
// token and no refresh credential, the Jazz route, and two pending deferred
// items due for the next Run Now. Their inputs are not held locally, so the
// receiving pass must read them.
func seedDueItems(t *testing.T, dbPath string) dueSeed {
	t.Helper()
	ctx := context.Background()
	seedBase(t, dbPath)
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()
	tokens, err := auth.NewSQLiteTokenStore(db, []byte(childKey))
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "local-test", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))

	routerStore, cfgStore := router.NewStore(db), config.NewStore(db)
	stateStore, err := state.NewStore(db)
	require.NoError(t, err)
	machine := state.NewMachine(stateStore)
	p := pipeline.NewPipeline(db, machine, stateStore, nil, nil, nil, nil, nil, nil, nil, pipeline.NewBroadcaster(),
		pipeline.WithVersioner(config.NewService(routerStore, cfgStore)), pipeline.WithConfigStore(cfgStore))
	origin, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	qs, err := queue.NewStore(db)
	require.NoError(t, err)
	var seed dueSeed
	created := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	for i, id := range []string{"A1", "B1"} {
		v, err := machine.DiscoverVideo(ctx, origin.ID, id, "UC1")
		require.NoError(t, err)
		seed.vids[i] = v.ID
		seed.items[i], err = qs.CreateDeferredItem(ctx, &queue.DeferredItem{
			VideoID: v.ID, RunID: origin.ID, Reason: queue.DeferredReasonQuotaExhausted,
			Status: queue.DeferredStatusPending, CreatedAt: created.Add(time.Duration(i) * time.Minute),
		})
		require.NoError(t, err)
	}
	require.NoError(t, stateStore.CompleteRun(ctx, origin.ID, 1, 0))
	seed.age = scalar[string](t, openReadDB(t, dbPath), `SELECT created_at FROM deferred_item WHERE id=?`, seed.items[0])
	return seed
}

func TestRunNowBoundAuthorizationRefusalWithStopWriteFaultEndsThroughW3(t *testing.T) {
	for _, c := range []struct {
		name  string
		fault bool
	}{
		{name: "authorization stop write refused", fault: true},
		{name: "control: no fault"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
			seed := seedDueItems(t, dbPath)
			db := openReadDB(t, dbPath)
			if c.fault {
				_, err := db.Exec(`CREATE TRIGGER refuse_authorization_stop BEFORE UPDATE OF dispatch_stop ON run
					WHEN NEW.dispatch_stop = 'authorization'
					BEGIN SELECT RAISE(ABORT, 'injected stop write fault'); END`)
				require.NoError(t, err)
			}
			provider := newAuthErrorProvider(t)

			srv := startServer(t, dbPath, provider.srv.URL)
			srv.waitOverview()
			eventually(t, runDoneBudget, "the scheduler to start", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })
			code, accepted := srv.postJSON("/api/runs")
			require.Equal(t, http.StatusAccepted, code, "%v", accepted)
			runID := int64(accepted["run_id"].(float64))
			require.Equal(t, 2, scalar[int](t, db, `SELECT COUNT(*) FROM deferred_item WHERE retry_run_id=?`, runID), "Run Now claimed both due items")
			eventually(t, runDoneBudget, "the run to end", func() bool {
				return scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID) != "running"
			})
			logs := srv.out.String()

			videos, _ := provider.videosListCount()
			require.Equal(t, 1, videos, "one receiving videos.list for both items, answered 401; no other receiving read\n%s", logs)
			require.Equal(t, 1, scalar[int](t, db, `SELECT COUNT(*) FROM quota_ledger WHERE run_id=? AND operation_type='videos_list'`, runID))
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM executed_action`))
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM error_entry`))
			require.Contains(t, logs, "reconnect_required", "the typed cause is logged")

			if !c.fault {
				require.Equal(t, "authorization", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, runID))
				for i, item := range seed.items {
					require.Equal(t, "deferred/authorization_pending", scalar[string](t, db,
						`SELECT final_outcome||'/'||deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, seed.vids[i]))
					require.Equal(t, "pending", scalar[string](t, db, `SELECT status FROM deferred_item WHERE id=?`, item))
				}
				return
			}
			require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, runID), logs)
			require.Equal(t, "accounting_or_integrity", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, runID), "W3 stored the integrity stop\n%s", logs)
			reason := scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, runID)
			require.Equal(t, "diag:v1 phase=unknown operation=account_quota code=sqlite_error sqlite=SQLITE_CONSTRAINT", reason, logs)
			code, body := srv.do(http.MethodGet, "/api/runs/"+itoa(runID))
			require.Equal(t, http.StatusOK, code, body)
			var detail map[string]any
			require.NoError(t, json.Unmarshal([]byte(body), &detail))
			require.Equal(t, reason, detail["interrupt_reason"], "the registered API says what the row says")
			require.NotContains(t, body, "injected", "no fixture or SQL text is served")
			require.Equal(t, 0, scalar[int](t, db, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, runID), "no settlement is claimed as committed")
			for _, item := range seed.items {
				require.Equal(t, "pending/quota_exhausted", scalar[string](t, db, `SELECT status||'/'||deferral_reason FROM deferred_item WHERE id=?`, item), "the exact work is retained")
			}
			require.Equal(t, seed.age, scalar[string](t, db, `SELECT created_at FROM deferred_item WHERE id=?`, seed.items[0]))
			srv.sigterm()
		})
	}
}
