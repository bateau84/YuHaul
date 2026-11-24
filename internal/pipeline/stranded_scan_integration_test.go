//go:build integration

package pipeline

import (
	"database/sql"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/state"
)

// Tests of the stranded scan (ARS-005 §3, §11.3 C1–C3, §11.5 E1 preview
// part). Real migrated SQLite and the real pipeline; the fake YouTube endpoint
// counts requests. Legacy evidence (version-0 runs, guards) is seeded because
// only pre-024 history produces those shapes.

// strandedSnapshot lists every table a scan must leave byte-identical (§11 "Snapshot").
var strandedSnapshot = []string{"video", "run", "deferred_item", "error_entry", "playlist_send_guard",
	"executed_action", "route_decision", "run_video_outcome", "transient_metadata", "quota_ledger"}

func (e *runNowEnv) scanStranded(t *testing.T) (StrandedReport, []StrandedVideo) {
	t.Helper()
	tx, err := e.db().BeginTx(e.ctx, &sql.TxOptions{ReadOnly: true})
	require.NoError(t, err)
	defer tx.Rollback()
	report, plan, err := scanStrandedTx(e.ctx, tx)
	require.NoError(t, err)
	return report, plan
}

// sRun inserts a production run. version 0 is a legacy run, 1 a guarded one.
func (e *runNowEnv) sRun(t *testing.T, runType, status string, version int, phase string) int64 {
	t.Helper()
	spec := seedSpec{startedAt: time.Now().UTC(), runType: runType, status: status, safety: version, authority: version}
	if phase != "" {
		spec.phase = sp(phase)
	}
	if status != "stopped" {
		return e.seedRun(t, spec)
	}
	// A stopped row can only be reached through a stop request on a running one.
	spec.status = "running"
	id := e.seedRun(t, spec)
	e.sExec(t, `UPDATE run SET stop_requested_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
	e.sExec(t, `UPDATE run SET status = 'stopped' WHERE id = ?`, id)
	return id
}

// sVideo inserts a known video. runID may be 0 (no discovering run).
func (e *runNowEnv) sVideo(t *testing.T, ext string, runID int64, processing string, final *string) int64 {
	t.Helper()
	var run any
	if runID != 0 {
		run = runID
	}
	res, err := e.db().ExecContext(e.ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome) VALUES (?,'UC1',?,?,?)`,
		ext, run, processing, final)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}

func (e *runNowEnv) sExec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := e.db().ExecContext(e.ctx, query, args...)
	require.NoError(t, err, query)
}

func (e *runNowEnv) sDeferred(t *testing.T, videoID, runID int64, status string, retryRun int64, createdAt string) {
	t.Helper()
	var retry any
	if retryRun != 0 {
		retry = retryRun
	}
	e.sExec(t, `INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at, retry_run_id) VALUES (?,?, 'quota_exhausted', 0, ?, ?, ?)`,
		videoID, runID, status, createdAt, retry)
}

func (e *runNowEnv) sError(t *testing.T, videoID, runID int64, status string, retryRun int64, createdAt string) {
	t.Helper()
	var retry any
	if retryRun != 0 {
		retry = retryRun
	}
	e.sExec(t, `INSERT INTO error_entry (video_id, run_id, error_category, error_message, status, created_at, retry_run_id) VALUES (?,?, 'runtime', 'seed', ?, ?, ?)`,
		videoID, runID, status, createdAt, retry)
}

func (e *runNowEnv) sGuard(t *testing.T, videoID, runID int64) {
	t.Helper()
	d := createRouteDecision(t, e.ctx, e.stateStore, videoID, runID, e.routeID, 1, state.RouteDecisionNotMatched)
	e.sExec(t, `INSERT INTO playlist_send_guard (video_id, target_playlist_id, action_type, run_id, route_decision_id, authorization_id, prepared_at) VALUES (?, 'PLseed', 'playlist_insert', ?, ?, 'seed-auth', ?)`,
		videoID, runID, d, time.Now().UTC().Format(time.RFC3339Nano))
}

func fp(s string) *string { return &s }

func reasonPairs(item BlockedVideo) []BlockReason { return item.Reasons }

// V2 seeds are the blocked/unblocked oracle: the scan must agree with the
// veto definitions in the seed table, name each video's reason and run, and
// keep the not-ended runs apart (their ownership outranks nothing blocked).
func TestStrandedScanV2SeedsBlockedWithReasonAndRun(t *testing.T) {
	e := newV2Env(t)
	e.seed(t)
	scan := func() (StrandedReport, []StrandedVideo) {
		tx, err := e.pipeline.db.BeginTx(e.ctx, &sql.TxOptions{ReadOnly: true})
		require.NoError(t, err)
		defer tx.Rollback()
		report, plan, err := scanStrandedTx(e.ctx, tx)
		require.NoError(t, err)
		return report, plan
	}
	id := func(name string) int64 { return e.videos[name].ID }
	runOf := func(name string) int64 {
		var run int64
		require.NoError(t, e.pipeline.db.QueryRowContext(e.ctx, `SELECT discovered_in_run_id FROM video WHERE id=?`, id(name)).Scan(&run))
		return run
	}
	// While the guarded evidence run is interrupted, the two unblocked seeds
	// belong to it: they are not ended, not stranded.
	report, plan := scan()
	want := map[string][]BlockReason{
		"branch1_matched_decision":      {{BlockAmbiguousLegacyEvidence, runOf("branch1_matched_decision")}},
		"branch1_missing_config":        {{BlockAmbiguousLegacyEvidence, runOf("branch1_missing_config")}},
		"branch1_missing_route":         {{BlockAmbiguousLegacyEvidence, runOf("branch1_missing_route")}},
		"branch2_unresolved_action":     {{BlockAmbiguousLegacyEvidence, runOf("branch2_unresolved_action")}},
		"branch3_not_acted_at_scan":     {{BlockAmbiguousLegacyEvidence, runOf("branch3_not_acted_at_scan")}},
		"held_guard":                    {{BlockUnresolvedSendGuard, e.evidence}},
		"uncertain_addition_with_guard": {{BlockUncertainAddition, e.evidence}},
	}
	require.Equal(t, len(want), report.Blocked.Videos)
	require.Len(t, report.Blocked.Items, len(want))
	var gotIDs []int64
	for _, item := range report.Blocked.Items {
		gotIDs = append(gotIDs, item.VideoID)
	}
	require.True(t, sort.SliceIsSorted(gotIDs, func(i, j int) bool { return gotIDs[i] < gotIDs[j] }), "items ordered by video id")
	for name, reasons := range want {
		var found *BlockedVideo
		for i := range report.Blocked.Items {
			if report.Blocked.Items[i].VideoID == id(name) {
				found = &report.Blocked.Items[i]
			}
		}
		require.NotNil(t, found, name)
		require.Equal(t, e.videos[name].VideoID, found.YouTubeVideoID, name)
		require.Equal(t, reasons, reasonPairs(*found), name)
	}
	require.Equal(t, map[string]int{BlockAmbiguousLegacyEvidence: 5, BlockUnresolvedSendGuard: 1, BlockUncertainAddition: 1}, report.Blocked.ByReason)
	// Branch 4's video is blocked by its legacy retry, but it also holds a
	// retrying Errors entry. An active queue item is the first category, so the
	// scan reports it as already queued (in the Errors queue), not blocked.
	require.Equal(t, 1, report.AlreadyQueued.Videos)
	require.Equal(t, 1, report.AlreadyQueued.InErrorsQueue)
	require.Empty(t, plan)
	require.Equal(t, 2, report.NotEnded.Videos)
	require.Equal(t, []RunVideos{{RunID: e.evidence, Videos: 2}}, report.NotEnded.Runs)

	// Once the evidence run has ended, the guard on an already-succeeded
	// target and the control have nothing blocking them: they are stranded,
	// with that run as origin. Every blocked seed stays blocked.
	_, err := e.pipeline.db.ExecContext(e.ctx, `UPDATE run SET status='failed' WHERE id=?`, e.evidence)
	require.NoError(t, err)
	report, plan = scan()
	require.Equal(t, []StrandedVideo{
		{VideoID: id("guard_on_succeeded_target"), OriginRunID: e.evidence},
		{VideoID: id("control_no_evidence"), OriginRunID: e.evidence},
	}, plan)
	require.Equal(t, 2, report.Requeue)
	require.Equal(t, len(want), report.Blocked.Videos)
	require.Zero(t, report.NotEnded.Videos)
	require.Empty(t, report.NotEnded.Runs)
}

// C1 (AC4): every category count is exact, the categories partition the
// unfinished videos, and the retained, finished and unavailable controls are
// counted only where the contract says. Category sizes differ so a video in
// the wrong category changes a count.
func TestStrandedScanCategoriesPartitionUnfinishedVideos(t *testing.T) {
	e := newRunNowEnv(t)
	failed := e.sRun(t, "manual", "failed", 1, "")
	completed := e.sRun(t, "scheduled", "completed", 1, "")
	stopped := e.sRun(t, "manual", "stopped", 1, "")
	interrupted := e.sRun(t, "scheduled", "interrupted", 1, "")
	legacyRunning := e.sRun(t, "manual", "running", 0, "discovery")
	legacyRunning2 := e.sRun(t, "scheduled", "running", 0, "discovery")
	dryRun := e.sRun(t, "dry_run", "completed", 1, "")
	old := "2026-01-01T00:00:00Z"
	new := "2026-02-01T00:00:00Z"

	// Stranded: unfinished in ended runs, one per unfinished case (5) plus
	// the other ended run kinds (3).
	var stranded []int64
	for i, st := range []string{"discovered", "not_yet_evaluated", "evaluating", "evaluated", "acted"} {
		stranded = append(stranded, e.sVideo(t, fmt.Sprintf("s-%d", i), failed, st, nil))
	}
	stranded = append(stranded, e.sVideo(t, "s-completed-run", completed, "not_yet_evaluated", nil))
	stranded = append(stranded, e.sVideo(t, "s-stopped-run", stopped, "evaluating", fp("not_yet_evaluated")))
	matched := e.sVideo(t, "s-matched-no-outcome", failed, "evaluated", nil)
	createRouteDecision(t, e.ctx, e.stateStore, matched, failed, e.routeID, 1, state.RouteDecisionMatched)
	stranded = append(stranded, matched)

	// Finished controls: no category at all.
	e.sVideo(t, "f-completed", failed, "completed", fp("accepted"))
	e.sVideo(t, "f-rejected", failed, "evaluated", fp("rejected"))
	e.sVideo(t, "f-excluded", failed, "evaluated", fp("excluded"))
	e.sVideo(t, "f-unavailable", failed, "completed", fp("unavailable_on_youtube"))

	// Committed deferred / failed outcomes: counted only, never re-queued.
	e.sVideo(t, "r-deferred", failed, "deferred", fp("deferred"))
	e.sVideo(t, "r-failed", failed, "failed", fp("failed"))
	removedRetained := e.sVideo(t, "r-failed-removed", failed, "failed", fp("failed"))
	e.sDeferred(t, removedRetained, failed, "discarded", 0, old)
	withItem := e.sVideo(t, "r-deferred-with-item", failed, "deferred", fp("deferred"))
	e.sDeferred(t, withItem, failed, "pending", 0, old)

	// Active queue items: pending and claimed deferred; active and claimed Errors.
	q1 := e.sVideo(t, "q-pending", failed, "not_yet_evaluated", nil)
	e.sDeferred(t, q1, failed, "pending", 0, old)
	q2 := e.sVideo(t, "q-claimed", failed, "not_yet_evaluated", nil)
	e.sDeferred(t, q2, failed, "retrying", interrupted, old)
	q3 := e.sVideo(t, "q-error-active", failed, "not_yet_evaluated", nil)
	e.sError(t, q3, failed, "active", 0, old)
	q4 := e.sVideo(t, "q-error-claimed", failed, "not_yet_evaluated", nil)
	e.sError(t, q4, failed, "retrying", interrupted, old)
	// A removed item older than a new pending one: queued, not removed.
	q5 := e.sVideo(t, "q-pending-after-removal", failed, "not_yet_evaluated", nil)
	e.sDeferred(t, q5, failed, "discarded", 0, old)
	e.sDeferred(t, q5, failed, "pending", 0, new)

	// Removed by the user: the most recent item was removed.
	rm1 := e.sVideo(t, "rm-deferred", failed, "not_yet_evaluated", nil)
	e.sDeferred(t, rm1, failed, "discarded", 0, old)
	rm2 := e.sVideo(t, "rm-error", failed, "not_yet_evaluated", nil)
	e.sError(t, rm2, failed, "dismissed", 0, old)

	// Not ended: an interrupted guarded run, an unended legacy run, and two
	// open runs at once (counted once per video, under each run).
	e.sVideo(t, "o-interrupted", interrupted, "not_yet_evaluated", nil)
	e.sVideo(t, "o-legacy-running", legacyRunning, "not_yet_evaluated", nil)
	e.sVideo(t, "o-legacy-running-b", legacyRunning, "evaluating", nil)
	twice := e.sVideo(t, "o-two-open-runs", interrupted, "not_yet_evaluated", nil)
	e.sExec(t, `INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (?,?,NULL,?)`, legacyRunning2, twice, time.Now().UTC().Format(time.RFC3339Nano))

	// No origin: the discovering run is gone, or only a dry run reached it.
	e.sVideo(t, "n-run-deleted", 0, "not_yet_evaluated", nil)
	e.sVideo(t, "n-dry-run-only", dryRun, "not_yet_evaluated", nil)

	// Blocked: a held send guard.
	g := e.sVideo(t, "b-guard", failed, "not_yet_evaluated", nil)
	e.sGuard(t, g, failed)

	report, plan := e.scanStranded(t)

	wantPlan := make([]StrandedVideo, 0, len(stranded))
	for _, id := range stranded {
		origin := failed
		switch id {
		case stranded[5]:
			origin = completed
		case stranded[6]:
			origin = stopped
		}
		wantPlan = append(wantPlan, StrandedVideo{VideoID: id, OriginRunID: origin})
	}
	require.Equal(t, wantPlan, plan, "stranded videos, ordered by video id, each with its origin run")
	require.Equal(t, 8, report.Requeue)

	require.Equal(t, 5, report.AlreadyQueued.Videos)
	require.Equal(t, 2, report.AlreadyQueued.InErrorsQueue)
	require.Equal(t, 2, report.RemovedByUser)
	require.Equal(t, 2, report.NoOriginRun)

	require.Equal(t, 1, report.Blocked.Videos)
	require.Equal(t, map[string]int{BlockUnresolvedSendGuard: 1}, report.Blocked.ByReason)
	require.Equal(t, []BlockedVideo{{VideoID: g, YouTubeVideoID: "b-guard", Reasons: []BlockReason{{BlockUnresolvedSendGuard, failed}}}}, report.Blocked.Items)

	require.Equal(t, 4, report.NotEnded.Videos)
	require.Equal(t, []RunVideos{{interrupted, 2}, {legacyRunning, 2}, {legacyRunning2, 1}}, report.NotEnded.Runs,
		"per-run counts may overlap: the video in two open runs is counted under each")

	require.Equal(t, 3, report.RetainedWithoutQueueItem.Videos)
	require.Equal(t, 1, report.RetainedWithoutQueueItem.RemovedByUser)

	// The categories partition the unfinished videos exactly.
	unfinished := e.count(t, `SELECT COUNT(*) FROM video WHERE processing_state <> 'completed' AND (final_outcome IS NULL OR final_outcome = 'not_yet_evaluated')`)
	require.Equal(t, 22, unfinished, "seed bookkeeping")
	require.Equal(t, unfinished, report.Requeue+report.AlreadyQueued.Videos+report.RemovedByUser+
		report.Blocked.Videos+report.NotEnded.Videos+report.NoOriginRun)
}

// C2: the origin is the newest production run that reached the video through
// any of the five links, and a link through a finished queue row still counts.
func TestStrandedScanOriginIsNewestProductionRun(t *testing.T) {
	e := newRunNowEnv(t)
	run14 := e.sRun(t, "manual", "failed", 0, "discovery") // a legacy run, ended
	run30 := e.sRun(t, "scheduled", "failed", 1, "")
	other := e.sRun(t, "manual", "failed", 1, "")

	both := e.sVideo(t, "c2-both", run14, "not_yet_evaluated", nil)
	e.sExec(t, `INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (?,?,NULL,?)`, run30, both, time.Now().UTC().Format(time.RFC3339Nano))
	only14 := e.sVideo(t, "c2-only-14", run14, "not_yet_evaluated", nil)

	viaDecision := e.sVideo(t, "c2-decision", 0, "evaluated", nil)
	createRouteDecision(t, e.ctx, e.stateStore, viaDecision, other, e.routeID, 1, state.RouteDecisionNotMatched)
	viaDeferred := e.sVideo(t, "c2-deferred", 0, "not_yet_evaluated", nil)
	e.sDeferred(t, viaDeferred, run14, "completed", other, "2026-01-01T00:00:00Z")
	viaError := e.sVideo(t, "c2-error", 0, "not_yet_evaluated", nil)
	e.sError(t, viaError, run14, "resolved", other, "2026-01-01T00:00:00Z")
	viaOutcome := e.sVideo(t, "c2-outcome", 0, "not_yet_evaluated", nil)
	e.sExec(t, `INSERT INTO run_video_outcome (run_id, video_id, final_outcome, updated_at) VALUES (?,?,NULL,?)`, other, viaOutcome, time.Now().UTC().Format(time.RFC3339Nano))

	report, plan := e.scanStranded(t)
	require.Equal(t, []StrandedVideo{
		{both, run30}, {only14, run14}, {viaDecision, other}, {viaDeferred, other}, {viaError, other}, {viaOutcome, other},
	}, plan)
	require.Equal(t, 6, report.Requeue)
	require.Zero(t, report.NoOriginRun)
}

// C3 (AC4): a scan changes nothing and makes no provider request.
func TestStrandedScanWritesNothingAndCallsNoProvider(t *testing.T) {
	e := newRunNowEnv(t)
	adm, err := e.pipeline.AdmitRunNow(e.ctx)
	require.NoError(t, err)
	adm.Execute(e.ctx) // real videos, decisions, actions and guards exist
	failed := e.sRun(t, "manual", "failed", 1, "")
	for i := 0; i < 5; i++ {
		e.sVideo(t, fmt.Sprintf("c3-%d", i), failed, "not_yet_evaluated", nil)
	}
	g := e.sVideo(t, "c3-guard", failed, "not_yet_evaluated", nil)
	e.sGuard(t, g, failed)

	before := e.tableDump(t, strandedSnapshot...)
	requests := e.server.Requests()
	report, _ := e.scanStranded(t)
	require.Equal(t, 5, report.Requeue)
	require.Equal(t, 1, report.Blocked.Videos)
	require.Equal(t, before, e.tableDump(t, strandedSnapshot...), "the scan wrote nothing")
	require.Equal(t, requests, e.server.Requests(), "the scan made no provider request")

	again, _ := e.scanStranded(t)
	require.Equal(t, report, again, "the scan is repeatable")
	require.Equal(t, before, e.tableDump(t, strandedSnapshot...), "a second scan wrote nothing either")
}

// E1, preview part (AC14): a run-14-shaped version-0 running run with 200
// unevaluated videos, 3 with a held guard and 1 whose deferred item it holds.
// Before Mark as ended the scan counts the rest under run 14 and offers none;
// after it, they are stranded with origin 14. The blocked and queued ones
// stay out.
func TestStrandedScanRun14ShapeBeforeAndAfterMarkAsEnded(t *testing.T) {
	e := newRunNowEnv(t)
	run14 := e.sRun(t, "manual", "running", 0, "discovery")

	tx, err := e.db().BeginTx(e.ctx, nil)
	require.NoError(t, err)
	var ids []int64
	for i := 0; i < 200; i++ {
		res, err := tx.ExecContext(e.ctx, `INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state) VALUES (?, 'UC1', ?, 'not_yet_evaluated')`, fmt.Sprintf("e1-%03d", i), run14)
		require.NoError(t, err)
		id, err := res.LastInsertId()
		require.NoError(t, err)
		ids = append(ids, id)
	}
	require.NoError(t, tx.Commit())
	for _, id := range ids[:3] {
		e.sGuard(t, id, run14)
	}
	e.sDeferred(t, ids[3], run14, "retrying", run14, "2026-01-01T00:00:00Z")

	report, plan := e.scanStranded(t)
	require.Empty(t, plan)
	require.Zero(t, report.Requeue)
	require.Equal(t, 196, report.NotEnded.Videos)
	require.Equal(t, []RunVideos{{RunID: run14, Videos: 196}}, report.NotEnded.Runs)
	require.Equal(t, 3, report.Blocked.Videos)
	require.Equal(t, map[string]int{BlockUnresolvedSendGuard: 3}, report.Blocked.ByReason)
	require.Equal(t, 1, report.AlreadyQueued.Videos)
	require.Zero(t, report.AlreadyQueued.InErrorsQueue)

	_, err = e.pipeline.EndLegacyRun(e.ctx, run14)
	require.NoError(t, err)

	report, plan = e.scanStranded(t)
	require.Len(t, plan, 196)
	for _, s := range plan {
		require.Equal(t, run14, s.OriginRunID)
	}
	require.Equal(t, 196, report.Requeue)
	require.Zero(t, report.NotEnded.Videos)
	require.Equal(t, 3, report.Blocked.Videos)
	require.Equal(t, 1, report.AlreadyQueued.Videos)
}
