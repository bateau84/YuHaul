package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/stretchr/testify/require"
)

// Review 771112e4 finding F2: the authorization half of the receiving
// precondition, through the real auth service, the real read dispatch and the
// real metadata manager. Only the OAuth token endpoint and the Data API are
// local servers, and a refusal reaches the manager in the shape production
// gives it (a wrapped primary error with no renewal observation).

const fixtureTokenKey = "01234567890123456789012345678901"

// setToken replaces the stored authorization with expired access and the given
// refresh credential, and rebinds the fixture's execution to it.
func (rf *receivingFixture) setToken(refresh string) {
	rf.t.Helper()
	ctx := context.Background()
	tokens, err := auth.NewSQLiteTokenStore(rf.db, []byte(fixtureTokenKey))
	require.NoError(rf.t, err)
	require.NoError(rf.t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "expired-access", RefreshToken: refresh,
		TokenType: "Bearer", Expiry: time.Now().Add(-time.Minute), Scopes: auth.DefaultScopes()}))
	rf.binding, err = rf.auth.Bind(ctx)
	require.NoError(rf.t, err)
	rf.p.executionBinding = &rf.binding
}

// captureLogs routes the pipeline's logger to the returned buffer.
func (rf *receivingFixture) captureLogs() *bytes.Buffer {
	var logs bytes.Buffer
	rf.p.logger = slog.New(slog.NewTextHandler(&logs, nil))
	return &logs
}

// restart is a process restart: the database handle is closed and reopened and
// every dependency (auth service, ledger, stores, manager, pipeline) is built
// fresh, so nothing in memory survives, neither the latch nor a known failure.
func (rf *receivingFixture) restart(path string) {
	rf.t.Helper()
	ctx := context.Background()
	require.NoError(rf.t, rf.db.Close())
	db, err := store.Open(ctx, path)
	require.NoError(rf.t, err)
	rf.t.Cleanup(func() { _ = db.Close() })
	tokens, err := auth.NewSQLiteTokenStore(db, []byte(fixtureTokenKey))
	require.NoError(rf.t, err)
	svc, err := auth.NewService(&auth.ServiceConfig{ClientID: "test-client", ClientSecret: "test-secret", RedirectURL: "http://localhost/callback",
		EncryptionKey: fixtureTokenKey, HTTPClient: &http.Client{Transport: rf.oauth}}, tokens, auth.NewInMemoryStateStore())
	require.NoError(rf.t, err)
	ss, err := state.NewStore(db)
	require.NoError(rf.t, err)
	g := *rf.guardedProductionFixture
	g.db, g.state, g.auth = db, ss, svc
	g.ledger = quota.New(db, quota.WithDefaultLimit(10_000))
	g.binding = auth.AuthorizationBinding{} // the new execution binds for itself
	rf.guardedProductionFixture = &g
	rf.compose(discardLogger())
}

func (rf *receivingFixture) requireNoProviderWork() {
	rf.t.Helper()
	require.Zero(rf.t, rf.listCalls.Load(), "no videos.list request")
	require.Zero(rf.t, rf.posts.Load(), "no playlist insert")
	require.Zero(rf.t, rf.ledgerRows(), "no debit for an unsent request")
}

func (rf *receivingFixture) errorEntries() int {
	rf.t.Helper()
	var n int
	require.NoError(rf.t, rf.db.QueryRow(`SELECT COUNT(*) FROM error_entry`).Scan(&n))
	return n
}

// Expired access that cannot be renewed settles every claimed receipt as
// authorization_pending, stores the stop with the first settlement, keeps the
// exact vetted cause, and makes no provider request. The refresh-less case is
// the one the review reproduced as failed/unknown_error with no stop.
func TestReceivingUnrenewableAccessSettlesAuthorizationPendingWithItsCause(t *testing.T) {
	for _, c := range []struct {
		name         string
		refresh      string
		oauthStatus  int
		wantCause    string
		wantRenewals int32
	}{
		{name: "no refresh credential", wantCause: "reconnect_required"},
		{name: "refresh rejected", refresh: "refresh", oauthStatus: http.StatusBadRequest, wantCause: "reconnect_required", wantRenewals: 1},
		{name: "provider unavailable", refresh: "refresh", oauthStatus: http.StatusServiceUnavailable, wantCause: "temporary_unavailable", wantRenewals: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			rf := newReceivingFixture(t)
			if c.oauthStatus != 0 {
				rf.oauthStatus.Store(int32(c.oauthStatus))
			}
			rf.setToken(c.refresh)
			logs := rf.captureLogs()

			// 120 inputs: three chunks. The first is refused, the other two are
			// an unsent suffix and must not be sent.
			receipts := rf.claimNew(120)
			require.NoError(t, rf.p.processDeferredItems(rf.ctx(), rf.recvRun, receipts, false))

			require.Equal(t, "authorization", rf.storedStop(), "committed with the first authorization_pending settlement")
			require.Equal(t, c.wantRenewals, rf.renewals.Load(), "at most one renewal attempt, no retry loop")
			rf.requireNoProviderWork()
			for _, r := range receipts {
				rf.requireDeferred(r, "authorization_pending")
			}
			require.Zero(t, rf.errorEntries(), "an authorization refusal is not a video failure")
			var runStatus string
			require.NoError(t, rf.db.QueryRow(`SELECT status FROM run WHERE id=?`, rf.recvRun).Scan(&runStatus))
			require.Equal(t, "running", runStatus)

			require.Equal(t, 1, strings.Count(logs.String(), "run authorization stop"), "one bounded line per failure, not per video")
			require.Contains(t, logs.String(), "authorization_cause="+c.wantCause)
			require.Contains(t, logs.String(), "inputs=120")
			require.NotContains(t, logs.String(), "recv-1")
			require.NotContains(t, logs.String(), "expired-access")
		})
	}
}

// The stop and the live receipts of an interrupted run survive a real process
// restart: the database is closed and reopened, every dependency is rebuilt,
// and the resumed run settles its owned receipts from the stored stop (or
// from the refusal) with no request, no renewal and no re-claim.
func TestReceivingLiveReceiptsSettleAfterReopen(t *testing.T) {
	for _, c := range []struct {
		name   string
		stored bool // the stop was committed before the restart
	}{
		{name: "stop stored before the restart", stored: true},
		{name: "crash before the first settlement", stored: false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "yuhaul.db")
			db, err := store.Open(ctx, path)
			require.NoError(t, err)
			rf := newReceivingFixtureOn(t, db)
			rf.setToken("") // expired access, nothing to renew it with
			receipts := rf.claimNew(3)
			if c.stored {
				require.NoError(t, rf.qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
					return rf.state.RaiseRunDispatchStopTx(ctx, tx, rf.recvRun, quota.StopAuthorization)
				}))
			}
			_, err = rf.db.Exec(`UPDATE run SET status='interrupted', current_phase=? WHERE id=?`, string(PhaseActionExecution), rf.recvRun)
			require.NoError(t, err)

			rf.restart(path)
			logs := rf.captureLogs()
			rf.p.Execute(ctx, rf.recvRun)

			require.Equal(t, "authorization", rf.storedStop())
			rf.requireNoProviderWork()
			require.Zero(t, rf.renewals.Load(), "no refresh credential: nothing to renew with")
			for _, r := range receipts {
				rf.requireDeferred(r, "authorization_pending")
			}
			require.Zero(t, rf.errorEntries())
			var status string
			require.NoError(t, rf.db.QueryRow(`SELECT status FROM run WHERE id=?`, rf.recvRun).Scan(&status))
			require.NotEqual(t, "failed", status, logs.String())
			stop, err := rf.ledger.RunDispatchStop(rf.ctx())
			require.NoError(t, err)
			require.Equal(t, quota.StopAuthorization, stop, "a fresh execution reads the stored stop back")
		})
	}
}

// When the execution cannot take a binding at all, receipts the run already
// owns still settle locally under an authorization stop, instead of being
// stranded by the early failure. A bind failure that is not an authorization
// condition is a local fault and settles nothing as authorization.
func TestReceivingRestoredReceiptsSettleWithoutABinding(t *testing.T) {
	for _, c := range []struct {
		name       string
		storedStop bool
		disconnect bool
		breakStore bool
		settles    bool
	}{
		{name: "stored stop, account disconnected", storedStop: true, disconnect: true, settles: true},
		{name: "no stored stop, account disconnected", disconnect: true, settles: true},
		{name: "local token-store fault is not an authorization refusal", breakStore: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			rf := newReceivingFixture(t)
			logs := rf.captureLogs()
			receipts := rf.claimNew(3)
			ready := rf.claimVideo(rf.videoID) // inputs held locally: evaluated, not fetched
			all := append(receipts, ready)
			if c.storedStop {
				_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='authorization' WHERE id=?`, rf.recvRun)
				require.NoError(t, err)
			}
			_, err := rf.db.Exec(`UPDATE run SET status='interrupted', current_phase=? WHERE id=?`, string(PhaseActionExecution), rf.recvRun)
			require.NoError(t, err)
			if c.disconnect {
				require.NoError(t, rf.auth.Disconnect(ctx))
				_, bindErr := rf.auth.Bind(ctx)
				require.ErrorIs(t, bindErr, auth.ErrNoConnectedAccount)
			}
			if c.breakStore {
				_, err := rf.db.Exec(`ALTER TABLE auth_tokens RENAME TO auth_tokens_broken`)
				require.NoError(t, err)
			}

			tokenCalls := rf.renewals.Load() // Disconnect's revocation also uses the token transport
			rf.p.Execute(ctx, rf.recvRun)

			rf.requireNoProviderWork()
			require.Equal(t, tokenCalls, rf.renewals.Load(), "no renewal during the run")
			var runStatus string
			require.NoError(t, rf.db.QueryRow(`SELECT status FROM run WHERE id=?`, rf.recvRun).Scan(&runStatus))
			require.Equal(t, "failed", runStatus, "the run still ends: it has no binding")
			if !c.settles {
				require.Empty(t, rf.storedStop(), "a local fault stores no authorization stop")
				var n int
				require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND deferred_reason='authorization_pending'`, rf.recvRun).Scan(&n))
				require.Zero(t, n)
				require.NotContains(t, logs.String(), "run authorization stop")
				return
			}
			require.Equal(t, "authorization", rf.storedStop())
			for _, r := range all {
				var outcome, reason sql.NullString
				require.NoError(t, rf.db.QueryRow(`SELECT final_outcome, deferred_reason FROM run_video_outcome WHERE run_id=? AND video_id=?`,
					rf.recvRun, r.VideoID).Scan(&outcome, &reason))
				require.Equal(t, "deferred", outcome.String, "video %d: %s", r.VideoID, logs.String())
				require.Equal(t, "authorization_pending", reason.String)
				var status string
				var owner sql.NullInt64
				require.NoError(t, rf.db.QueryRow(`SELECT status, retry_run_id FROM deferred_item WHERE id=?`, r.Deferred[0].ID).Scan(&status, &owner))
				require.Equal(t, "pending", status)
				require.Equal(t, rf.recvRun, owner.Int64, "pending again, keeping its most recent owner (ARS-005 6.2)")
			}
			require.Zero(t, rf.errorEntries())
			require.Contains(t, logs.String(), "run authorization stop")
			require.Contains(t, logs.String(), "authorization_cause=")
		})
	}
}

// Composition of the accounting-stop and authorization corrections: when the
// run already holds an accounting_or_integrity stop, an authorization-caused
// bind refusal must not settle its receipts as authorization_pending or lower
// the stored stop. Accounting outranks authorization (OC-001, ARS-005 6.1.3);
// the run ends through the ordinary failure path.
func TestReceivingBindRefusalYieldsToAnAccountingStop(t *testing.T) {
	ctx := context.Background()
	rf := newReceivingFixture(t)
	logs := rf.captureLogs()
	rf.claimNew(3)
	_, err := rf.db.Exec(`UPDATE run SET dispatch_stop='accounting_or_integrity', status='interrupted', current_phase=? WHERE id=?`,
		string(PhaseActionExecution), rf.recvRun)
	require.NoError(t, err)
	require.NoError(t, rf.auth.Disconnect(ctx))
	_, bindErr := rf.auth.Bind(ctx)
	require.ErrorIs(t, bindErr, auth.ErrNoConnectedAccount)

	tokenCalls := rf.renewals.Load()
	rf.p.Execute(ctx, rf.recvRun)

	rf.requireNoProviderWork()
	require.Equal(t, tokenCalls, rf.renewals.Load(), "no renewal during the run")
	require.Equal(t, "accounting_or_integrity", rf.storedStop(), "the higher stop is never lowered")
	var n int
	require.NoError(t, rf.db.QueryRow(`SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND deferred_reason='authorization_pending'`, rf.recvRun).Scan(&n))
	require.Zero(t, n, "no receipt settles as an authorization refusal under an accounting stop")
	var runStatus string
	require.NoError(t, rf.db.QueryRow(`SELECT status FROM run WHERE id=?`, rf.recvRun).Scan(&runStatus))
	require.Equal(t, "failed", runStatus)
	require.NotContains(t, logs.String(), "run authorization stop")
}
