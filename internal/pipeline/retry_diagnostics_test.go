package pipeline

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/duplicate"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/stretchr/testify/require"
)

func TestRetryPreservesAuxiliaryDiagnostics(t *testing.T) {
	ctx := context.Background()
	f := newGuardedProductionFixtureWithRenewalStatus(t, []string{"PLguarded-fixture"}, 503)
	defer f.db.Close()
	tokens, err := auth.NewSQLiteTokenStore(f.db, []byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	require.NoError(t, tokens.Save(ctx, &auth.TokenRecord{Provider: "youtube", AccessToken: "private_access_marker", RefreshToken: "private_refresh_marker", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour), Scopes: auth.DefaultScopes()}))
	binding, err := f.auth.Bind(ctx)
	require.NoError(t, err)
	qs, err := queue.NewStore(f.db)
	require.NoError(t, err)
	source, err := qs.CreateErrorEntry(ctx, &queue.ErrorEntry{VideoID: f.videoID, RunID: f.runID, Category: queue.ErrorCategoryRuntime, Message: "existing failure", Status: queue.ErrorStatusActive, CreatedAt: time.Now().UTC()})
	require.NoError(t, err)
	rs, cs := router.NewStore(f.db), config.NewStore(f.db)
	versioner := config.NewService(rs, cs)
	run := &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusRunning, ActionAuthorityVersion: 1}
	run.ConfigVersionID, err = versioner.ActiveVersionID(ctx)
	require.NoError(t, err)
	var receipt queue.RetryGroupReceipt
	require.NoError(t, qs.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var e error
		run.ID, e = f.state.CreateGuardedMutationRunTx(ctx, tx, run, true)
		if e != nil {
			return e
		}
		var claimed bool
		receipt, _, claimed, e = qs.ClaimErrorGroupReceiptTx(ctx, tx, source, run.ID, time.Now())
		if e != nil {
			return e
		}
		if !claimed {
			return errors.New("not claimed")
		}
		return nil
	}))
	_, err = f.db.Exec(`CREATE TRIGGER followup_reject_ledger BEFORE INSERT ON quota_ledger BEGIN SELECT RAISE(ABORT,'private_sqlite_marker'); END`)
	require.NoError(t, err)
	var posts int
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.WriteHeader(401)
		io.WriteString(w, `{"error":{"errors":[{"reason":"authError"}]}}`)
	}))
	defer provider.Close()
	e := NewExecutor(f.db, f.ledger, f.auth, WithYouTubeBaseURL(provider.URL))
	var logs bytes.Buffer
	p := &Pipeline{db: f.db, stateStore: f.state, machine: state.NewMachine(f.state), queueStore: qs, auth: f.auth, versioner: versioner, configStore: cs, router: router.NewEngine(rs), detector: duplicate.NewDetector(f.db), meta: metadata.NewManager(metadata.NewStore(f.db), nil, nil), logger: slog.New(slog.NewTextHandler(&logs, nil)), executor: e, executionBinding: &binding}
	p.router.WithRunRoutesValidator(p.ValidateRunRoutes)
	err = p.processRetryVideo(context.WithValue(ctx, guardedRetryGroupKey{}, receipt), run.ID, f.videoID)
	require.NoError(t, err, "a committed retry failure handoff is handled")
	var guards, rows int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM playlist_send_guard WHERE run_id=?`, run.ID).Scan(&guards))
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM quota_ledger WHERE run_id=?`, run.ID).Scan(&rows))
	t.Logf("processRetryVideo error=%v posts=%d guards=%d ledger_rows=%d logs=%s", err, posts, guards, rows, logs.String())
	require.Equal(t, 1, posts)
	require.Equal(t, 1, guards)
	require.Zero(t, rows)
	var outcome, processing, sourceStatus string
	require.NoError(t, f.db.QueryRow(`SELECT final_outcome, processing_state FROM video WHERE id=?`, f.videoID).Scan(&outcome, &processing))
	require.Equal(t, "failed", outcome)
	require.Equal(t, "failed", processing)
	require.NoError(t, f.db.QueryRow(`SELECT status FROM error_entry WHERE id=?`, source).Scan(&sourceStatus))
	require.Equal(t, "resolved", sourceStatus)
	var retained int
	require.NoError(t, f.db.QueryRow(`SELECT count(*) FROM error_entry WHERE run_id=? AND video_id=? AND status='active' AND error_category='runtime'`, run.ID, f.videoID).Scan(&retained))
	require.Equal(t, 1, retained)
	require.Contains(t, logs.String(), "status=401")
	require.Contains(t, logs.String(), "reason=authError")
	require.Contains(t, logs.String(), "renewal_category=temporary_provider", "retry composition must not discard auxiliary failure categories")
	require.Contains(t, logs.String(), "accounting_diagnostic=")
	require.Contains(t, logs.String(), "code=sqlite_error")
	require.NotContains(t, logs.String(), "private_")
}
