package pipeline

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

// Final POST admission fence (authorization ARS-001, "Authority at guard
// preparation and POST admission", "Exact receipt and admission verification").
// Real migrated SQLite, claim, auth.Service, quota ledger, executor and YouTube
// adapter; only the OAuth and Data API transports are local. Channels are the
// only synchronisation: the renewal and the provider response are held and
// released by the test, with no sleeps.

type admissionOpts struct {
	kind       string // "error" (default) or "deferred" source
	blockOAuth bool   // hold the token renewal until releaseOAuth
	blockPost  bool   // hold the provider response until releasePost
	postStatus int    // default 200
	bound      time.Duration
	nativeOnly bool // no retry claim: a native send of the origin run
}

type admissionScene struct {
	t        *testing.T
	ctx      context.Context
	path     string
	f        *guardedProductionFixture
	qs       *queue.Store
	e        *Executor
	p        *Pipeline
	recv     int64 // the sending run
	source   int64
	receipt  queue.RetryGroupReceipt
	matches  []RouteMatch
	opts     admissionOpts
	posts    atomic.Int32
	server   *httptest.Server
	oauthHit chan struct{}
	oauthGo  chan struct{}
	postHit  chan struct{}
	postGo   chan struct{}
	once     [4]sync.Once
}

type execResult struct {
	outcome state.VideoFinalOutcome
	err     error
}

func newAdmissionScene(t *testing.T, o admissionOpts) *admissionScene {
	t.Helper()
	ctx := context.Background()
	s := &admissionScene{t: t, ctx: ctx, opts: o, path: filepath.Join(t.TempDir(), "fence.db"),
		oauthHit: make(chan struct{}), oauthGo: make(chan struct{}), postHit: make(chan struct{}), postGo: make(chan struct{})}
	if o.kind == "" {
		s.opts.kind = "error"
	}
	if o.postStatus == 0 {
		s.opts.postStatus = http.StatusOK
	}
	db, err := store.Open(ctx, s.path)
	require.NoError(t, err)
	oauth := roundTripTestFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "oauth2.googleapis.com" {
			t.Errorf("unexpected OAuth endpoint %q", req.URL.Host)
		}
		if s.opts.blockOAuth {
			s.once[0].Do(func() { close(s.oauthHit) })
			<-s.oauthGo
		}
		return responseTest(http.StatusOK, `{"access_token":"fresh-access","token_type":"Bearer","expires_in":3600}`), nil
	})
	s.f = newGuardedProductionFixtureWithOAuth(t, db, []string{"PLguarded-fixture"}, http.StatusOK, oauth)
	t.Cleanup(func() { _ = s.f.db.Close() })
	s.qs, err = queue.NewStore(s.f.db)
	require.NoError(t, err)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.posts.Add(1)
		if s.opts.blockPost {
			s.once[1].Do(func() { close(s.postHit) })
			<-s.postGo
		}
		w.Header().Set("Content-Type", "application/json")
		if s.opts.postStatus != http.StatusOK {
			w.WriteHeader(s.opts.postStatus)
			_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"backendError"}]}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"item","snippet":{"playlistId":"PLguarded-fixture","resourceId":{"videoId":"guarded-external-video"}}}`)
	}))
	t.Cleanup(s.server.Close)
	opts := []ExecutorOption{WithYouTubeBaseURL(s.server.URL)}
	if o.bound > 0 {
		opts = append(opts, WithAdmissionWriterBound(o.bound))
	}
	s.e = NewExecutor(s.f.db, s.f.ledger, s.f.auth, opts...)
	s.p = &Pipeline{db: s.f.db, stateStore: s.f.state, machine: state.NewMachine(s.f.state), queueStore: s.qs, logger: discardLogger()}
	if o.nativeOnly {
		s.recv = s.f.runID
		s.matches = []RouteMatch{{RouteDecisionID: s.f.decisionID, RouteID: s.f.routeID}}
		return s
	}

	versioner := config.NewService(router.NewStore(s.f.db), config.NewStore(s.f.db))
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
	run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	claim := func(tx *sql.Tx) (bool, error) {
		if s.opts.kind == "deferred" {
			var taken bool
			var err error
			s.receipt, _, taken, err = s.qs.ClaimDeferredGroupReceiptTx(ctx, tx, s.source, run.ID, time.Now())
			return taken, err
		}
		var taken bool
		var err error
		s.receipt, _, taken, err = s.qs.ClaimErrorGroupReceiptTx(ctx, tx, s.source, run.ID, time.Now())
		return taken, err
	}
	if s.opts.kind == "deferred" {
		s.source, err = s.qs.CreateDeferredItem(ctx, &queue.DeferredItem{VideoID: s.f.videoID, RunID: s.f.runID, Reason: queue.DeferredReasonQuotaExhausted, Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC()})
	} else {
		s.source, err = s.qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: s.f.videoID, RunID: s.f.runID, Category: queue.ErrorCategoryRuntime, Message: "retry source", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	}
	require.NoError(t, err)
	require.NoError(t, s.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		if run.ID, err = s.f.state.CreateGuardedMutationRunTx(ctx, tx, run, true); err != nil {
			return err
		}
		taken, err := claim(tx)
		if err == nil && !taken {
			err = errRetryClaimLost
		}
		return err
	}))
	s.recv = run.ID
	decision := createRouteDecision(t, ctx, s.f.state, s.f.videoID, s.recv, s.f.routeID, 1, state.RouteDecisionMatched)
	s.matches = []RouteMatch{{RouteDecisionID: decision, RouteID: s.f.routeID}}
	return s
}

func (s *admissionScene) start() <-chan execResult {
	ctx := context.WithValue(s.ctx, guardedRetryGroupKey{}, s.receipt)
	if s.opts.nativeOnly {
		ctx = s.ctx
	}
	return s.startWith(ctx, s.e)
}

func (s *admissionScene) startWith(ctx context.Context, e *Executor) <-chan execResult {
	done := make(chan execResult, 1)
	go func() {
		out, err := e.ExecuteBound(ctx, s.recv, s.f.videoID, s.matches, s.f.binding)
		done <- execResult{out, err}
	}()
	return done
}

func (s *admissionScene) wait(done <-chan execResult) execResult {
	s.t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(30 * time.Second):
		s.t.Fatal("execution did not finish")
		return execResult{}
	}
}

func (s *admissionScene) reached(ch <-chan struct{}, what string) {
	s.t.Helper()
	select {
	case <-ch:
	case <-time.After(30 * time.Second):
		s.t.Fatalf("%s not reached", what)
	}
}

func (s *admissionScene) releaseOAuth() { s.once[2].Do(func() { close(s.oauthGo) }) }
func (s *admissionScene) releasePost()  { s.once[3].Do(func() { close(s.postGo) }) }

func (s *admissionScene) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	require.NoError(s.t, s.f.db.QueryRow(query, args...).Scan(&n))
	return n
}

func (s *admissionScene) guards() int {
	return s.count(`SELECT count(*) FROM playlist_send_guard WHERE run_id=? AND video_id=?`, s.recv, s.f.videoID)
}

func (s *admissionScene) ledgerRows() int { return s.count(`SELECT count(*) FROM quota_ledger`) }

func (s *admissionScene) actions() int {
	return s.count(`SELECT count(*) FROM executed_action WHERE run_id=? AND video_id=?`, s.recv, s.f.videoID)
}

func (s *admissionScene) removeSource() {
	s.t.Helper()
	if s.opts.kind == "deferred" {
		require.NoError(s.t, s.qs.DiscardDeferredItem(s.ctx, s.source))
		return
	}
	require.NoError(s.t, s.qs.DismissErrorEntry(s.ctx, s.source))
}

func (s *admissionScene) markStop() {
	s.t.Helper()
	_, err := s.f.db.Exec(`UPDATE run SET stop_requested_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), s.recv)
	require.NoError(s.t, err)
}

// onlyOneUnitFits makes the ledger hold exactly one insertion, so a leaked
// provisional hold would be visible as a refused Acquire.
func (s *admissionScene) capacityIsFree() {
	s.t.Helper()
	require.NoError(s.t, s.f.ledger.SetDailyLimit(s.ctx, s.e.insertCost))
	hold, err := s.f.ledger.Acquire(s.ctx, quota.OpPlaylistItemsInsert)
	require.NoError(s.t, err)
	require.NotNil(s.t, hold, "a provisional hold leaked")
	require.NoError(s.t, hold.Cancel())
}

// removal or ownership loss committed after the guard commit and before acceptance.
func TestAdmissionFenceRefusesAfterRemovalOrLossBeforeAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    string
		disturb func(*admissionScene)
		want    error
	}{
		{"dismiss the claimed error", "error", func(s *admissionScene) { s.removeSource() }, queue.ErrWorkRemoved},
		{"discard the claimed deferred item", "deferred", func(s *admissionScene) { s.removeSource() }, queue.ErrWorkRemoved},
		{"another owner takes the row", "error", func(s *admissionScene) {
			_, err := s.f.db.Exec(`UPDATE error_entry SET retry_run_id=? WHERE id=?`, s.f.runID, s.source)
			require.NoError(s.t, err)
		}, queue.ErrWorkNotOwned},
		{"unexpected same-owner sibling appears", "error", func(s *admissionScene) {
			id, err := s.qs.CreateErrorEntry(s.ctx, &queue.ErrorEntry{VideoID: s.f.videoID, RunID: s.f.runID, Category: queue.ErrorCategoryRuntime, Message: "later", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
			require.NoError(s.t, err)
			_, err = s.f.db.Exec(`UPDATE error_entry SET status='retrying', retry_run_id=? WHERE id=?`, s.recv, id)
			require.NoError(s.t, err)
		}, queue.ErrWorkNotOwned},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newAdmissionScene(t, admissionOpts{kind: tc.kind, blockOAuth: true})
			require.NoError(t, s.f.ledger.SetDailyLimit(s.ctx, s.e.insertCost))
			done := s.start()
			s.reached(s.oauthHit, "token renewal")
			require.Equal(t, 1, s.guards(), "the guard committed before renewal")
			tc.disturb(s)
			s.releaseOAuth()
			r := s.wait(done)

			var refusal *retryAdmissionRefusal
			require.ErrorAs(t, r.err, &refusal)
			require.ErrorIs(t, r.err, tc.want)
			require.Zero(t, s.posts.Load(), "no POST")
			require.Zero(t, s.ledgerRows(), "no charge")
			require.Zero(t, s.actions(), "no attempt evidence, no tail evidence")
			require.Equal(t, 1, s.guards(), "the prepared guard is kept")
			s.capacityIsFree()
			require.Zero(t, s.count(`SELECT count(*) FROM error_entry WHERE run_id=? AND id<>?`, s.recv, s.source), "no replacement sink")
			require.Zero(t, s.count(`SELECT count(*) FROM deferred_item WHERE status='pending'`), "no replacement deferral")

			// The typed refusal routes through run ending: the removed or lost
			// video is protected, so nothing is recreated and the guard stays.
			s.p.registerRun(s.recv)
			s.p.failRun(s.ctx, s.recv, "retry failed", r.err)
			s.p.finishRun(s.ctx, s.recv)
			require.Equal(t, 1, s.guards())
			require.Zero(t, s.count(`SELECT count(*) FROM deferred_item WHERE status='pending'`))
			require.Zero(t, s.posts.Load())
		})
	}
}

// Stop committed before the context is cancelled: typed Stop, no POST, no
// charge, this invocation's own guard removed through the no-attempt path.
func TestAdmissionFenceStopBeforeCancellation(t *testing.T) {
	s := newAdmissionScene(t, admissionOpts{blockOAuth: true})
	require.NoError(t, s.f.ledger.SetDailyLimit(s.ctx, s.e.insertCost))
	done := s.start()
	s.reached(s.oauthHit, "token renewal")
	s.markStop()
	s.releaseOAuth()
	r := s.wait(done)

	require.NoError(t, s.ctx.Err(), "the context was never cancelled")
	var refusal *retryAdmissionRefusal
	require.ErrorAs(t, r.err, &refusal)
	require.ErrorIs(t, r.err, state.ErrPlaylistSendStopped)
	require.Zero(t, s.posts.Load())
	require.Zero(t, s.ledgerRows())
	require.Zero(t, s.guards(), "own guard removed after proven non-admission")
	require.Equal(t, 1, s.count(`SELECT count(*) FROM executed_action WHERE run_id=? AND video_id=? AND outcome='not_attempted' AND executed_at IS NULL`, s.recv, s.f.videoID))
	s.capacityIsFree()

	// Run ending: the marked Stop settles through the receipt-aware handoff.
	s.p.registerRun(s.recv)
	s.p.failRun(s.ctx, s.recv, "retry failed", r.err)
	s.p.finishRun(s.ctx, s.recv)
	run, err := s.f.state.GetRun(s.ctx, s.recv)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusStopped, run.Status)
	require.Equal(t, 1, s.count(`SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending'`, s.f.videoID), "stopped handoff")
}

// Stop and removal both present at the check: Stop owns the run ending, and the
// stopped handoff leaves the removed row removed.
func TestAdmissionFenceStopAndRemovalTogether(t *testing.T) {
	s := newAdmissionScene(t, admissionOpts{blockOAuth: true})
	done := s.start()
	s.reached(s.oauthHit, "token renewal")
	s.removeSource()
	s.markStop()
	s.releaseOAuth()
	r := s.wait(done)

	require.ErrorIs(t, r.err, state.ErrPlaylistSendStopped)
	require.NotErrorIs(t, r.err, queue.ErrWorkRemoved)
	require.Zero(t, s.guards())
	s.p.registerRun(s.recv)
	s.p.failRun(s.ctx, s.recv, "retry failed", r.err)
	s.p.finishRun(s.ctx, s.recv)
	run, err := s.f.state.GetRun(s.ctx, s.recv)
	require.NoError(t, err)
	require.Equal(t, state.RunStatusStopped, run.Status)
	var status string
	require.NoError(t, s.f.db.QueryRow(`SELECT status FROM error_entry WHERE id=?`, s.source).Scan(&status))
	require.Equal(t, "dismissed", status, "removed work stays removed")
	require.Zero(t, s.count(`SELECT count(*) FROM deferred_item WHERE video_id=? AND status='pending'`, s.f.videoID), "removed work is not recreated")
}

// The inverse order: acceptance wins, then removal and Stop commit while the
// provider response is held. The attempt is accounted once, truthfully.
func TestAdmissionFenceAdmissionWinsThenRemovalOrStop(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusInternalServerError} {
		for _, later := range []string{"removal", "stop", "both"} {
			t.Run(fmt.Sprintf("%d/%s", status, later), func(t *testing.T) {
				s := newAdmissionScene(t, admissionOpts{blockPost: true, postStatus: status})
				done := s.start()
				s.reached(s.postHit, "provider request")
				require.Equal(t, int32(1), s.posts.Load())
				require.Zero(t, s.ledgerRows(), "accounting happens once, after the response")
				if later != "stop" {
					s.removeSource()
				}
				if later != "removal" {
					s.markStop()
				}
				s.releasePost()
				r := s.wait(done)

				require.Equal(t, int32(1), s.posts.Load(), "exactly one POST")
				require.Equal(t, 1, s.ledgerRows(), "exactly one consumption entry")
				var category string
				require.NoError(t, s.f.db.QueryRow(`SELECT outcome_category FROM quota_ledger`).Scan(&category))
				if status == http.StatusOK {
					require.NoError(t, r.err)
					require.Equal(t, "success", category)
					require.Zero(t, s.guards(), "definitive success resolves the owned guard")
					require.Equal(t, 1, s.count(`SELECT count(*) FROM executed_action WHERE run_id=? AND video_id=? AND outcome='succeeded' AND executed_at IS NOT NULL`, s.recv, s.f.videoID))
				} else {
					require.Error(t, r.err)
					var refusal *retryAdmissionRefusal
					require.False(t, errors.As(r.err, &refusal), "an admitted attempt is not an admission refusal")
					require.NotEqual(t, "success", category)
					require.Equal(t, 1, s.guards(), "uncertain evidence keeps the guard")
					require.Equal(t, 1, s.count(`SELECT count(*) FROM executed_action WHERE run_id=? AND video_id=? AND outcome='failed' AND outcome_reason='mutation_application_uncertain' AND executed_at IS NOT NULL`, s.recv, s.f.videoID))
				}
			})
		}
	}
}

// The writer cannot be had. Cancel while waiting for it, and a bounded wait
// that runs out: neither admits, charges, leaks a hold or loses the guard
// except through the existing no-attempt release for a cancelled request.
func TestAdmissionFenceWriterWait(t *testing.T) {
	// The test takes the SQLite writer from the executor's own goroutine,
	// after renewal has saved its token and just before the admission scope
	// asks for it, so the scope's wait is the one under test.
	var releaseWriter func()
	takeWriter := func(s *admissionScene, taken chan<- struct{}) {
		s.e.beforeAdmissionWriter = func() {
			tx, err := s.f.db.BeginTx(s.ctx, nil)
			if err != nil {
				t.Errorf("hold writer: %v", err)
				return
			}
			if _, err := tx.Exec(`UPDATE run SET id = id WHERE id = -1`); err != nil {
				t.Errorf("hold writer: %v", err)
			}
			releaseWriter = func() {
				if err := tx.Rollback(); err != nil {
					t.Errorf("release writer: %v", err)
				}
			}
			close(taken)
		}
	}
	t.Run("request cancelled while waiting for the writer", func(t *testing.T) {
		s := newAdmissionScene(t, admissionOpts{bound: 20 * time.Second})
		require.NoError(t, s.f.ledger.SetDailyLimit(s.ctx, s.e.insertCost))
		taken := make(chan struct{})
		takeWriter(s, taken)
		ctx, cancel := context.WithCancel(s.ctx)
		defer cancel()
		done := s.startWith(context.WithValue(ctx, guardedRetryGroupKey{}, s.receipt), s.e)
		s.reached(taken, "writer held elsewhere")
		cancel()
		r := s.wait(done) // returns while the writer is still held elsewhere
		releaseWriter()
		require.ErrorIs(t, r.err, context.Canceled)
		require.Zero(t, s.posts.Load())
		require.Zero(t, s.ledgerRows())
		s.capacityIsFree()
	})
	t.Run("bounded wait runs out", func(t *testing.T) {
		s := newAdmissionScene(t, admissionOpts{bound: 200 * time.Millisecond})
		require.NoError(t, s.f.ledger.SetDailyLimit(s.ctx, s.e.insertCost))
		taken := make(chan struct{})
		takeWriter(s, taken)
		r := s.wait(s.start())
		releaseWriter()
		require.Error(t, r.err)
		require.Contains(t, r.err.Error(), "guard kept")
		require.False(t, errors.Is(r.err, queue.ErrWorkRemoved) || errors.Is(r.err, state.ErrPlaylistSendStopped), "local failure is not removal or Stop")
		require.Zero(t, s.posts.Load())
		require.Zero(t, s.ledgerRows())
		require.Equal(t, 1, s.guards(), "local failure keeps the guard")
		require.Zero(t, s.actions())
		s.capacityIsFree()
	})
}

// Crash and reopen: the guard persists; a restarted process repeats nothing
// and invents no admission, whichever side of acceptance the crash fell on.
func TestAdmissionFenceCrashAndReopen(t *testing.T) {
	for _, stage := range []string{"after guard preparation", "after admission before settlement"} {
		t.Run(stage, func(t *testing.T) {
			afterAdmission := stage == "after admission before settlement"
			s := newAdmissionScene(t, admissionOpts{blockOAuth: !afterAdmission, blockPost: afterAdmission})
			done := s.start()
			if afterAdmission {
				s.reached(s.postHit, "provider request")
			} else {
				s.reached(s.oauthHit, "token renewal")
			}
			// "Crash": a restarted process opens the same file and looks.
			db2, err := store.Open(s.ctx, s.path)
			require.NoError(t, err)
			defer db2.Close()
			var guards, ledger, attempts int
			require.NoError(t, db2.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, s.recv).Scan(&guards))
			require.NoError(t, db2.QueryRow(`SELECT count(*) FROM quota_ledger`).Scan(&ledger))
			require.NoError(t, db2.QueryRow(`SELECT count(*) FROM executed_action WHERE run_id=?`, s.recv).Scan(&attempts))
			require.Equal(t, 1, guards)
			require.Zero(t, ledger, "no invented admission or result")
			require.Zero(t, attempts)
			postsAtCrash := s.posts.Load()

			qs2, err := queue.NewStore(db2)
			require.NoError(t, err)
			receipt, err := qs2.RetryGroupReceiptForRunVideo(s.ctx, s.recv, s.f.videoID)
			require.NoError(t, err)
			require.Equal(t, s.receipt, receipt, "the stored claim survives reopen")
			restarted := NewExecutor(db2, s.f.ledger, s.f.auth, WithYouTubeBaseURL(s.server.URL))
			_, err = restarted.ExecuteBound(context.WithValue(s.ctx, guardedRetryGroupKey{}, receipt), s.recv, s.f.videoID, s.matches, s.f.binding)
			require.Error(t, err, "the unresolved guard blocks the target")
			require.Equal(t, postsAtCrash, s.posts.Load(), "no blind repeat")

			s.releaseOAuth()
			s.releasePost()
			s.wait(done)
		})
	}
}

// Native sends are unchanged by the fence: an ordinary send still goes out, and
// a durable Stop marker before acceptance removes only this invocation's guard
// with the plain typed Stop, not a retry refusal.
func TestAdmissionFenceNativeSendUnchanged(t *testing.T) {
	t.Run("native send is admitted once", func(t *testing.T) {
		s := newAdmissionScene(t, admissionOpts{nativeOnly: true})
		r := s.wait(s.start())
		require.NoError(t, r.err)
		require.Equal(t, int32(1), s.posts.Load())
		require.Equal(t, 1, s.ledgerRows())
		require.Zero(t, s.guards())
	})
	t.Run("native Stop before acceptance", func(t *testing.T) {
		s := newAdmissionScene(t, admissionOpts{nativeOnly: true, blockOAuth: true})
		done := s.start()
		s.reached(s.oauthHit, "token renewal")
		s.markStop()
		s.releaseOAuth()
		r := s.wait(done)
		require.ErrorIs(t, r.err, state.ErrPlaylistSendStopped)
		var refusal *retryAdmissionRefusal
		require.False(t, errors.As(r.err, &refusal))
		require.Zero(t, s.posts.Load())
		require.Zero(t, s.ledgerRows())
		require.Zero(t, s.guards())
	})
}

// A guard that is no longer this invocation's (here: replaced by another
// prepared_at) refuses admission as a local integrity failure: no POST, no
// charge, no inference of removal or Stop.
func TestAdmissionFenceRefusesAChangedGuard(t *testing.T) {
	s := newAdmissionScene(t, admissionOpts{blockOAuth: true})
	require.NoError(t, s.f.ledger.SetDailyLimit(s.ctx, s.e.insertCost))
	done := s.start()
	s.reached(s.oauthHit, "token renewal")
	_, err := s.f.db.Exec(`UPDATE playlist_send_guard SET prepared_at='2000-01-01T00:00:00Z' WHERE run_id=?`, s.recv)
	require.NoError(t, err)
	s.releaseOAuth()
	r := s.wait(done)
	require.ErrorIs(t, r.err, state.ErrPlaylistGuardLost)
	var refusal *retryAdmissionRefusal
	require.False(t, errors.As(r.err, &refusal), "a changed guard is not removal")
	require.Zero(t, s.posts.Load())
	require.Zero(t, s.ledgerRows())
	require.Equal(t, 1, s.guards(), "the other guard is untouched")
	s.capacityIsFree()
}
