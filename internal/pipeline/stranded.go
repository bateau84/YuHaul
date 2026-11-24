package pipeline

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// The stranded scan of ARS-005 §3 (BR-037 AC4, AC15). It reads, in one
// transaction the caller opens read-only, every unfinished known video and
// assigns it to exactly one category. It writes nothing and calls no
// provider. PreviewStranded and RequeueStranded (§4) are built on it; the
// scan itself takes no lock and knows nothing about runs that are executing
// in this process.

// Block reasons of a blocked video (ARS-005 §3.2, BR-037 AC15).
const (
	BlockUncertainAddition       = "uncertain_addition"
	BlockUnresolvedSendGuard     = "unresolved_send_guard"
	BlockAmbiguousLegacyEvidence = "ambiguous_legacy_evidence"
)

// strandedNotEndedRunStatusList is the run statuses that have not ended: a
// stuck legacy run is `running`, a resumable one `interrupted`.
const strandedNotEndedRunStatusList = `'running','interrupted'`

// StrandedVideo is one video the confirm would re-queue and the run whose
// evidence it carries (ARS-005 §3.3; origin per C2: the newest production run
// that reached it).
type StrandedVideo struct{ VideoID, OriginRunID int64 }

// BlockReason is one piece of evidence that blocks a video, with the run
// whose evidence it is.
type BlockReason struct {
	Reason string
	RunID  int64
}

// BlockedVideo is a blocked video with every reason that blocks it.
type BlockedVideo struct {
	VideoID        int64
	YouTubeVideoID string
	Reasons        []BlockReason
}

// RunVideos is the number of not-ended videos one run still owns. Counts of
// different runs may overlap.
type RunVideos struct {
	RunID  int64
	Videos int
}

// StrandedReport is the scan's answer. Every unfinished video is in exactly
// one of: Requeue, AlreadyQueued, RemovedByUser, Blocked, NotEnded and
// NoOriginRun. RetainedWithoutQueueItem is a separate count of videos with a
// committed deferred or failed outcome; the action never re-queues them.
type StrandedReport struct {
	Requeue, RemovedByUser, NoOriginRun int
	AlreadyQueued                       struct{ Videos, InErrorsQueue int }
	Blocked                             struct {
		Videos   int
		ByReason map[string]int
		Items    []BlockedVideo
	}
	NotEnded struct {
		Videos int
		Runs   []RunVideos
	}
	RetainedWithoutQueueItem struct{ Videos, RemovedByUser int }
}

// strandedCTEs are the common table expressions of §3.1. The subject rows of
// `unfinished` carry the columns the shared predicates read: a NULL target,
// receiving run 0 and assume_acted = 1, so the veto is evaluated as it will
// be at send time (ARS-005 §2.1, critic F1).
const strandedCTEs = `
prod AS MATERIALIZED (SELECT id, status FROM run WHERE run_type IN ('manual','scheduled')),
legacy_run AS MATERIALIZED (SELECT * FROM run WHERE run_type IN ('manual','scheduled') AND mutation_safety_version = 0),
unfinished AS MATERIALIZED (
  SELECT v.id AS video_id, v.video_id AS youtube_video_id,
         CAST(NULL AS TEXT) AS target_playlist_id, 0 AS receiving_run_id, 1 AS assume_acted
  FROM video v
  WHERE v.processing_state <> 'completed'
    AND (v.final_outcome IS NULL OR v.final_outcome = 'not_yet_evaluated')),
member(video_id, run_id) AS MATERIALIZED (
  SELECT u.video_id, v.discovered_in_run_id FROM unfinished u JOIN video v ON v.id = u.video_id WHERE v.discovered_in_run_id IS NOT NULL
  UNION SELECT rd.video_id, rd.run_id FROM route_decision rd JOIN unfinished u ON u.video_id = rd.video_id
  UNION SELECT d.video_id, d.retry_run_id FROM deferred_item d JOIN unfinished u ON u.video_id = d.video_id WHERE d.retry_run_id IS NOT NULL
  UNION SELECT e.video_id, e.retry_run_id FROM error_entry e JOIN unfinished u ON u.video_id = e.video_id WHERE e.retry_run_id IS NOT NULL
  UNION SELECT o.video_id, o.run_id FROM run_video_outcome o JOIN unfinished u ON u.video_id = o.video_id),
prod_member AS MATERIALIZED (SELECT m.video_id, m.run_id, p.status FROM member m JOIN prod p ON p.id = m.run_id)`

// strandedCategoriesQuery is ARS-005 §3.1 verbatim, built from the shared
// predicates so the scan and the real send path evaluate one definition.
const strandedCategoriesQuery = `WITH` + strandedCTEs + `
SELECT subject.video_id, subject.youtube_video_id,
  ` + queue.ActiveQueueItem + ` AS queued,
  ` + queue.ErrorsQueueItem + ` AS in_errors,
  ` + queue.RemovedByUser + ` AS removed,
  EXISTS (` + state.HeldSendGuard + `)
    OR EXISTS (SELECT 1 FROM legacy_run old_run WHERE ` + state.LegacySendVeto + `) AS blocked,
  EXISTS (SELECT 1 FROM prod_member pm WHERE pm.video_id = subject.video_id
          AND pm.status IN (` + strandedNotEndedRunStatusList + `)) AS open_run,
  (SELECT MAX(pm.run_id) FROM prod_member pm WHERE pm.video_id = subject.video_id) AS origin_run_id
FROM unfinished subject
ORDER BY subject.video_id`

// strandedHeldGuardsQuery lists, for each blocked video (?1: JSON array of
// video ids), its held send guards as [run_id, uncertain] pairs. The guard
// text is the shared HeldSendGuard, correlated on subject.video_id.
const strandedHeldGuardsQuery = `WITH subject(video_id) AS (SELECT CAST(j.value AS INTEGER) FROM json_each(?1) j)
SELECT subject.video_id,
  (SELECT json_group_array(json_array(held.run_id, held.uncertain)) FROM (` + state.HeldSendGuard + `) held)
FROM subject
ORDER BY subject.video_id`

// strandedLegacyVetoQuery lists the version-0 runs whose evidence makes
// LegacySendVeto true, for each blocked video (?1: JSON array of video ids).
const strandedLegacyVetoQuery = `WITH legacy_run AS MATERIALIZED (SELECT * FROM run WHERE run_type IN ('manual','scheduled') AND mutation_safety_version = 0),
subject AS (SELECT CAST(j.value AS INTEGER) AS video_id, CAST(NULL AS TEXT) AS target_playlist_id,
                   0 AS receiving_run_id, 1 AS assume_acted FROM json_each(?1) j)
SELECT subject.video_id, old_run.id
FROM subject JOIN legacy_run old_run ON ` + state.LegacySendVeto + `
ORDER BY subject.video_id, old_run.id`

// strandedNotEndedQuery counts, per run that has not ended, the videos of
// that run that are in the not-ended category (?1: JSON array of video ids).
const strandedNotEndedQuery = `WITH` + strandedCTEs + `
SELECT pm.run_id, COUNT(DISTINCT pm.video_id)
FROM prod_member pm
WHERE pm.status IN (` + strandedNotEndedRunStatusList + `)
  AND pm.video_id IN (SELECT CAST(j.value AS INTEGER) FROM json_each(?1) j)
GROUP BY pm.run_id
ORDER BY pm.run_id`

// strandedRetainedQuery counts known videos with a committed deferred or
// failed outcome and no active queue item, and how many of those the user
// removed. They are only counted (BR-037 AC4).
const strandedRetainedQuery = `SELECT COUNT(*), COALESCE(SUM(` + queue.RemovedByUser + `), 0)
FROM (SELECT id AS video_id FROM video WHERE final_outcome IN ('deferred','failed')) subject
WHERE NOT ` + queue.ActiveQueueItem

type strandedRow struct {
	videoID                                     int64
	youtubeID                                   string
	queued, inErrors, removed, blocked, openRun bool
	origin                                      sql.NullInt64
}

// scanStrandedTx runs the categories query and the detail queries in tx and
// returns the report and the plan (the stranded videos, ordered by video id).
// tx should be read-only: the scan issues only SELECT statements.
func scanStrandedTx(ctx context.Context, tx *sql.Tx) (StrandedReport, []StrandedVideo, error) {
	var report StrandedReport
	report.Blocked.ByReason = map[string]int{}
	report.Blocked.Items = []BlockedVideo{}
	report.NotEnded.Runs = []RunVideos{}

	rows, err := readStrandedRows(ctx, tx)
	if err != nil {
		return StrandedReport{}, nil, err
	}

	plan := []StrandedVideo{}
	var blockedIDs, notEndedIDs []int64
	youtubeID := map[int64]string{}
	for _, r := range rows {
		switch {
		case r.queued:
			report.AlreadyQueued.Videos++
			if r.inErrors {
				report.AlreadyQueued.InErrorsQueue++
			}
		case r.removed:
			report.RemovedByUser++
		case r.blocked:
			blockedIDs = append(blockedIDs, r.videoID)
			youtubeID[r.videoID] = r.youtubeID
		case r.openRun:
			notEndedIDs = append(notEndedIDs, r.videoID)
		case !r.origin.Valid:
			report.NoOriginRun++
		default:
			plan = append(plan, StrandedVideo{VideoID: r.videoID, OriginRunID: r.origin.Int64})
		}
	}
	report.Requeue = len(plan)
	report.Blocked.Videos = len(blockedIDs)
	report.NotEnded.Videos = len(notEndedIDs)

	if len(blockedIDs) > 0 {
		items, err := readBlockedDetails(ctx, tx, blockedIDs, youtubeID)
		if err != nil {
			return StrandedReport{}, nil, err
		}
		report.Blocked.Items = items
		for _, item := range items {
			report.Blocked.ByReason[item.Reasons[0].Reason]++
		}
	}
	if len(notEndedIDs) > 0 {
		runs, err := readNotEndedRuns(ctx, tx, notEndedIDs)
		if err != nil {
			return StrandedReport{}, nil, err
		}
		report.NotEnded.Runs = runs
	}
	err = tx.QueryRowContext(ctx, strandedRetainedQuery).
		Scan(&report.RetainedWithoutQueueItem.Videos, &report.RetainedWithoutQueueItem.RemovedByUser)
	if err != nil {
		return StrandedReport{}, nil, fmt.Errorf("stranded scan: retained count: %w", err)
	}
	return report, plan, nil
}

func readStrandedRows(ctx context.Context, tx *sql.Tx) ([]strandedRow, error) {
	rs, err := tx.QueryContext(ctx, strandedCategoriesQuery)
	if err != nil {
		return nil, fmt.Errorf("stranded scan: categories: %w", err)
	}
	defer rs.Close()
	var out []strandedRow
	for rs.Next() {
		var r strandedRow
		if err := rs.Scan(&r.videoID, &r.youtubeID, &r.queued, &r.inErrors, &r.removed, &r.blocked, &r.openRun, &r.origin); err != nil {
			return nil, fmt.Errorf("stranded scan: categories: %w", err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("stranded scan: categories: %w", err)
	}
	return out, nil
}

// readBlockedDetails names every reason and run behind each blocked video.
// Reasons are ordered uncertain_addition, unresolved_send_guard,
// ambiguous_legacy_evidence and then by run id; an identical reason and run
// is listed once. A blocked video with no reason means the detail queries
// and the categories query disagree, which is an error, not an empty list.
func readBlockedDetails(ctx context.Context, tx *sql.Tx, ids []int64, youtubeID map[int64]string) ([]BlockedVideo, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("stranded scan: blocked ids: %w", err)
	}
	reasons := map[int64]map[BlockReason]bool{}
	add := func(videoID int64, r BlockReason) {
		if reasons[videoID] == nil {
			reasons[videoID] = map[BlockReason]bool{}
		}
		reasons[videoID][r] = true
	}

	guards, err := tx.QueryContext(ctx, strandedHeldGuardsQuery, string(list))
	if err != nil {
		return nil, fmt.Errorf("stranded scan: held guards: %w", err)
	}
	defer guards.Close()
	for guards.Next() {
		var videoID int64
		var pairs sql.NullString
		if err := guards.Scan(&videoID, &pairs); err != nil {
			return nil, fmt.Errorf("stranded scan: held guards: %w", err)
		}
		if !pairs.Valid {
			continue
		}
		var held [][2]int64
		if err := json.Unmarshal([]byte(pairs.String), &held); err != nil {
			return nil, fmt.Errorf("stranded scan: held guards: %w", err)
		}
		for _, h := range held {
			reason := BlockUnresolvedSendGuard
			if h[1] != 0 {
				reason = BlockUncertainAddition
			}
			add(videoID, BlockReason{Reason: reason, RunID: h[0]})
		}
	}
	if err := guards.Err(); err != nil {
		return nil, fmt.Errorf("stranded scan: held guards: %w", err)
	}
	if err := guards.Close(); err != nil {
		return nil, fmt.Errorf("stranded scan: held guards: %w", err)
	}

	legacy, err := tx.QueryContext(ctx, strandedLegacyVetoQuery, string(list))
	if err != nil {
		return nil, fmt.Errorf("stranded scan: legacy evidence: %w", err)
	}
	defer legacy.Close()
	for legacy.Next() {
		var videoID, runID int64
		if err := legacy.Scan(&videoID, &runID); err != nil {
			return nil, fmt.Errorf("stranded scan: legacy evidence: %w", err)
		}
		add(videoID, BlockReason{Reason: BlockAmbiguousLegacyEvidence, RunID: runID})
	}
	if err := legacy.Err(); err != nil {
		return nil, fmt.Errorf("stranded scan: legacy evidence: %w", err)
	}

	rank := map[string]int{BlockUncertainAddition: 0, BlockUnresolvedSendGuard: 1, BlockAmbiguousLegacyEvidence: 2}
	items := make([]BlockedVideo, 0, len(ids))
	for _, id := range ids {
		if len(reasons[id]) == 0 {
			return nil, fmt.Errorf("stranded scan: video %d is blocked but no guard or legacy evidence names it", id)
		}
		item := BlockedVideo{VideoID: id, YouTubeVideoID: youtubeID[id]}
		for r := range reasons[id] {
			item.Reasons = append(item.Reasons, r)
		}
		sort.Slice(item.Reasons, func(i, j int) bool {
			a, b := item.Reasons[i], item.Reasons[j]
			if rank[a.Reason] != rank[b.Reason] {
				return rank[a.Reason] < rank[b.Reason]
			}
			return a.RunID < b.RunID
		})
		items = append(items, item)
	}
	return items, nil
}

func readNotEndedRuns(ctx context.Context, tx *sql.Tx, ids []int64) ([]RunVideos, error) {
	list, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("stranded scan: not-ended ids: %w", err)
	}
	rs, err := tx.QueryContext(ctx, strandedNotEndedQuery, string(list))
	if err != nil {
		return nil, fmt.Errorf("stranded scan: not-ended runs: %w", err)
	}
	defer rs.Close()
	runs := []RunVideos{}
	for rs.Next() {
		var r RunVideos
		if err := rs.Scan(&r.RunID, &r.Videos); err != nil {
			return nil, fmt.Errorf("stranded scan: not-ended runs: %w", err)
		}
		runs = append(runs, r)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("stranded scan: not-ended runs: %w", err)
	}
	return runs, nil
}
