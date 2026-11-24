//go:build integration

package main

// Assessment d3160532, finding e1e3351e, through the real server composition
// (ARS-005 section 6.1.3; authorization ARS-001 retained-disposition rule 7).
// Startup recovery finds two live, locally ready receipts and no account. A
// trigger commits the queue discard's status change on the first route decision
// of V1 (after the pass validated the receipt, before W1 revalidates it), and,
// in the compound case, the standalone authorization stop transaction fails at
// COMMIT. A pure removal keeps its ordinary ending; a removal beside a storage
// fault ends the run through W3 with the registered SQLite reason, the removed
// row stays removed and the peer's exact work stays queued.

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
	"github.com/bateau84/YuHaul/internal/store"
)

type readyPairSeed struct {
	runID, item1, item2, video1, video2 int64
	created                             string
}

// seedReadyPair leaves an interrupted receiving run that owns two claims, V1
// then V2, both with inputs held locally and no evaluation, and no account.
func seedReadyPair(t *testing.T, dbPath string) readyPairSeed {
	t.Helper()
	ctx := context.Background()
	seedBase(t, dbPath)
	db, err := store.Open(ctx, dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`DELETE FROM auth_tokens`)
	require.NoError(t, err)
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
	var videos [2]*state.Video
	var items [2]int64
	created := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	for i, id := range []string{"V1", "V2"} {
		videos[i], err = machine.DiscoverVideo(ctx, origin.ID, id, "UC1")
		require.NoError(t, err)
		title, channel, description, duration := "Jazz "+id, "Test Channel", "d", 120
		_, err = metadata.NewStore(db).UpsertFetched(ctx, []metadata.TransientMetadata{{
			VideoID: id, Title: &title, ChannelName: &channel, Description: &description, DurationSeconds: &duration,
		}}, 24*time.Hour, time.Now())
		require.NoError(t, err)
		items[i], err = qs.CreateDeferredItem(ctx, &queue.DeferredItem{
			VideoID: videos[i].ID, RunID: origin.ID, Reason: queue.DeferredReasonQuotaExhausted,
			Status: queue.DeferredStatusPending, CreatedAt: created.Add(time.Duration(i) * time.Minute),
		})
		require.NoError(t, err)
	}
	require.NoError(t, stateStore.CompleteRun(ctx, origin.ID, 1, 0))
	recv, err := p.StartRun(ctx, state.RunTypeManual)
	require.NoError(t, err)
	for _, item := range items {
		require.Equal(t, recv.ID, scalar[int64](t, db, `SELECT retry_run_id FROM deferred_item WHERE id=?`, item))
	}
	require.NoError(t, stateStore.InterruptRun(ctx, recv.ID, "test: process stopped"))
	return readyPairSeed{runID: recv.ID, item1: items[0], item2: items[1], video1: videos[0].ID, video2: videos[1].ID, created: created.Format(time.RFC3339)}
}

func discardV1OnItsFirstDecision(t *testing.T, dbPath string, videoID int64) {
	t.Helper()
	_, err := openReadDB(t, dbPath).Exec(`CREATE TRIGGER discard_on_first_decision AFTER INSERT ON route_decision WHEN NEW.video_id = ` + itoa(videoID) + `
		BEGIN UPDATE deferred_item SET status='discarded' WHERE video_id=NEW.video_id AND status='retrying'; END`)
	require.NoError(t, err)
}

func failAuthorizationStopCommit(t *testing.T, dbPath string) {
	t.Helper()
	db := openReadDB(t, dbPath)
	for _, stmt := range []string{
		`CREATE TABLE fixture_parent(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE fixture_child(parent_id INTEGER REFERENCES fixture_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
		`CREATE TRIGGER fail_authorization_stop_commit AFTER UPDATE OF dispatch_stop ON run WHEN NEW.dispatch_stop='authorization'
			BEGIN INSERT INTO fixture_child(parent_id) VALUES(999); END`,
	} {
		_, err := db.Exec(stmt)
		require.NoError(t, err, stmt)
	}
}

func TestStartupRecoveryRemovedClaimBesideStopCommitFaultFailsClosed(t *testing.T) {
	for _, c := range []struct {
		name    string
		fault   bool
		storage bool // the run ends with the storage fault as its reason
	}{
		{name: "removed claim plus failing standalone stop commit", fault: true, storage: true},
		{name: "pure removed claim keeps its ordinary ending"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "yuhaul.db")
			seed := seedReadyPair(t, dbPath)
			discardV1OnItsFirstDecision(t, dbPath, seed.video1)
			if c.fault {
				failAuthorizationStopCommit(t, dbPath)
			}
			yt := newFakeYouTube(t, "V1", "V2")
			db := openReadDB(t, dbPath)

			srv := startServer(t, dbPath, yt.srv.URL)
			eventually(t, runDoneBudget, "startup recovery to finish", func() bool { return strings.Contains(srv.out.String(), "scheduler started") })
			logs := srv.out.String()

			require.Equal(t, "failed", scalar[string](t, db, `SELECT status FROM run WHERE id=?`, seed.runID), logs)
			reason := scalar[string](t, db, `SELECT interrupt_reason FROM run WHERE id=?`, seed.runID)
			if c.storage {
				require.Equal(t, "diag:v1 phase=unknown operation=account_quota code=sqlite_error sqlite=SQLITE_CONSTRAINT", reason, logs)
				require.Equal(t, "accounting_or_integrity", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, seed.runID))
			} else {
				require.NotContains(t, reason, "sqlite_error", logs)
				require.Equal(t, "authorization", scalar[string](t, db, `SELECT dispatch_stop FROM run WHERE id=?`, seed.runID), "the stop is stored on its own")
			}
			code, apiBody := srv.do(http.MethodGet, "/api/runs/"+itoa(seed.runID))
			require.Equal(t, http.StatusOK, code, apiBody)
			var detail map[string]any
			require.NoError(t, json.Unmarshal([]byte(apiBody), &detail))
			if c.storage {
				require.Equal(t, reason, detail["interrupt_reason"], "the registered API says what the row says")
				require.NotContains(t, apiBody, "injected")
				require.NotContains(t, apiBody, "fixture_child", "no SQL or table text is served")
			}
			require.Equal(t, "discarded", scalar[string](t, db, `SELECT status FROM deferred_item WHERE id=?`, seed.item1), "the removed row stays removed")
			require.Equal(t, 1, scalar[int](t, db, `SELECT COUNT(*) FROM deferred_item WHERE video_id=?`, seed.video1), "and is not recreated")
			require.Equal(t, "pending/quota_exhausted", scalar[string](t, db, `SELECT status||'/'||deferral_reason FROM deferred_item WHERE id=?`, seed.item2), "the peer's exact work is retained")
			require.Equal(t, 0, scalar[int](t, db, `SELECT COUNT(*) FROM run_video_outcome WHERE run_id=? AND final_outcome IS NOT NULL`, seed.runID),
				"no deferral is claimed as committed")
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM error_entry`))
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM executed_action`))
			require.Zero(t, scalar[int](t, db, `SELECT COUNT(*) FROM quota_ledger WHERE run_id=?`, seed.runID))
			require.Zero(t, yt.requestCount(), "no provider request\n%s", logs)
			srv.sigterm()
		})
	}
}
