package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Shared classification predicates of BR-039 (run-initiation ARS-006 §2).
// Each is SQL text that refers to the alias `v` (a video row). Callers never
// edit the text, so every consumer evaluates one definition. "Production"
// means run_type IN ('manual','scheduled').

// FinalOutcomes lists the video outcomes that are final: never changed, never
// re-routed, never re-sent. 'completed' is a processing state, not an outcome.
const FinalOutcomes = `'accepted','rejected','excluded','unavailable_on_youtube'`

// FinalVideo is true for a video whose final_outcome is final. A NULL outcome
// is not final; COALESCE keeps the predicate two-valued, so NOT FinalVideo is
// never NULL.
const FinalVideo = `(COALESCE(v.final_outcome, '') IN (` + FinalOutcomes + `))`

// prodSuccess is the condition for an executed_action alias `s` that is a
// succeeded playlist insert made by a production run. Simulation successes
// never count.
const prodSuccess = `s.action_type = 'playlist_insert' AND s.outcome = 'succeeded'
  AND EXISTS (SELECT 1 FROM run sr WHERE sr.id = s.run_id AND sr.run_type IN ('manual','scheduled'))`

// PartlyDoneVideo is true for a non-final video with at least one production
// playlist insert that succeeded.
const PartlyDoneVideo = `(NOT ` + FinalVideo + ` AND EXISTS (SELECT 1 FROM executed_action s
  WHERE s.video_id = v.id AND ` + prodSuccess + `))`

// RoutableByRun is true for a video the production run named by the bound
// parameter :run (sql.Named("run", id)) may route. It is false for a final
// video, for a video with a production success under another run's decision,
// and for a video held by a queue row the run does not itself own.
const RoutableByRun = `(NOT ` + FinalVideo + `
  AND NOT EXISTS (SELECT 1 FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
      WHERE s.video_id = v.id AND ` + prodSuccess + ` AND srd.run_id <> :run)
  AND NOT EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = v.id
      AND (d.status = 'pending' OR (d.status = 'retrying' AND (d.retry_run_id IS NULL OR d.retry_run_id <> :run))))
  AND NOT EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = v.id
      AND (e.status = 'active' OR (e.status = 'retrying' AND (e.retry_run_id IS NULL OR e.retry_run_id <> :run)))))`

// VideoClass is the BR-039 class of a video.
type VideoClass string

const (
	VideoClassFinal      VideoClass = "final"
	VideoClassPartlyDone VideoClass = "partly_done"
	VideoClassOpen       VideoClass = "open"
)

// Querier is satisfied by *sql.DB and *sql.Tx.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ClassifyVideoTx classifies one video. The outcome is the video's stored
// final_outcome ("" when NULL); it is the final outcome only for VideoClassFinal.
func ClassifyVideoTx(ctx context.Context, q Querier, videoID int64) (VideoClass, VideoFinalOutcome, error) {
	var outcome sql.NullString
	var final, partly bool
	err := q.QueryRowContext(ctx,
		`SELECT v.final_outcome, `+FinalVideo+`, `+PartlyDoneVideo+` FROM video v WHERE v.id = ?`,
		videoID).Scan(&outcome, &final, &partly)
	if err != nil {
		return "", "", fmt.Errorf("classifying video %d: %w", videoID, err)
	}
	switch {
	case final:
		return VideoClassFinal, VideoFinalOutcome(outcome.String), nil
	case partly:
		return VideoClassPartlyDone, VideoFinalOutcome(outcome.String), nil
	}
	return VideoClassOpen, VideoFinalOutcome(outcome.String), nil
}

// RouteDecisionRef names one matched route decision of the original run.
type RouteDecisionRef struct{ RouteDecisionID, RouteID int64 }

// OriginalDecision is the production decision that a partly done video
// continues: the single run holding all its production successes and that
// run's matched decisions, ordered by evaluation_order, id.
type OriginalDecision struct {
	RunID   int64
	Matched []RouteDecisionRef
}

// ErrOriginalDecisionUnavailable means the original decision cannot be
// identified: successes under several runs, a non-production run, or no
// matched decision.
var ErrOriginalDecisionUnavailable = errors.New("original decision unavailable")

// OriginalDecisionTx returns the original decision of a partly done video, or
// ErrOriginalDecisionUnavailable. Snapshot readability is checked separately
// by decisionAuthority.
func OriginalDecisionTx(ctx context.Context, q Querier, videoID int64) (OriginalDecision, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT DISTINCT srd.run_id FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
		 WHERE s.video_id = ? AND `+prodSuccess, videoID)
	if err != nil {
		return OriginalDecision{}, fmt.Errorf("reading successful decision runs of video %d: %w", videoID, err)
	}
	var runIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return OriginalDecision{}, fmt.Errorf("scanning decision run: %w", err)
		}
		runIDs = append(runIDs, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return OriginalDecision{}, fmt.Errorf("iterating decision runs: %w", err)
	}
	if err := rows.Close(); err != nil {
		return OriginalDecision{}, fmt.Errorf("closing decision runs: %w", err)
	}
	if len(runIDs) != 1 {
		return OriginalDecision{}, ErrOriginalDecisionUnavailable
	}
	od := OriginalDecision{RunID: runIDs[0]}

	var production bool
	if err := q.QueryRowContext(ctx, `SELECT run_type IN ('manual','scheduled') FROM run WHERE id = ?`, od.RunID).Scan(&production); err != nil {
		return OriginalDecision{}, fmt.Errorf("reading run %d type: %w", od.RunID, err)
	}
	if !production {
		return OriginalDecision{}, ErrOriginalDecisionUnavailable
	}

	mrows, err := q.QueryContext(ctx,
		`SELECT id, route_id FROM route_decision WHERE run_id = ? AND video_id = ? AND decision = 'matched'
		 ORDER BY evaluation_order, id`, od.RunID, videoID)
	if err != nil {
		return OriginalDecision{}, fmt.Errorf("reading matched decisions of run %d: %w", od.RunID, err)
	}
	defer func() { _ = mrows.Close() }()
	for mrows.Next() {
		var ref RouteDecisionRef
		if err := mrows.Scan(&ref.RouteDecisionID, &ref.RouteID); err != nil {
			return OriginalDecision{}, fmt.Errorf("scanning matched decision: %w", err)
		}
		od.Matched = append(od.Matched, ref)
	}
	if err := mrows.Err(); err != nil {
		return OriginalDecision{}, fmt.Errorf("iterating matched decisions: %w", err)
	}
	if len(od.Matched) == 0 {
		return OriginalDecision{}, ErrOriginalDecisionUnavailable
	}
	return od, nil
}
