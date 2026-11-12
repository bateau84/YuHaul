package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const playlistInsert = "playlist_insert"

type PlaylistSendClaim struct {
	VideoID, RunID, RouteDecisionID   int64
	TargetPlaylistID, AuthorizationID string
	ActionType                        string
	// DecisionRunID is the run whose matched decision authorizes a continued
	// send (ARS-006 §7.3). 0, or a value equal to RunID, means the sending run
	// made the decision. It is carried only so callers can log it; it is never
	// persisted and ResolvePlaylistSendTx must not read it.
	DecisionRunID int64
}

// continuedAuthorityQuery replaces the same-run authority query when a retry
// run continues another run's decision (ARS-006 §7.3, §7.4 C2/C4).
const continuedAuthorityQuery = `SELECT 1 FROM route_decision rd
JOIN run dr ON dr.id = rd.run_id
JOIN config_version cv ON cv.id = dr.config_version_id
WHERE rd.id = :decision AND rd.run_id = :decisionRun AND rd.video_id = :video AND rd.decision = 'matched'
  AND dr.run_type IN ('manual','scheduled')
  AND EXISTS (SELECT 1 FROM json_each(cv.snapshot,'$.routes') route, json_each(route.value,'$.actions') action
      WHERE CAST(json_extract(route.value,'$.id') AS INTEGER) = rd.route_id
        AND json_extract(action.value,'$.action_type') = 'playlist_insert'
        AND json_extract(action.value,'$.target_playlist_id') = :target)
  AND EXISTS (SELECT 1 FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
      WHERE s.video_id = :video AND ` + prodSuccess + ` AND srd.run_id = :decisionRun)
  AND NOT EXISTS (SELECT 1 FROM executed_action s JOIN route_decision srd ON srd.id = s.route_decision_id
      WHERE s.video_id = :video AND ` + prodSuccess + ` AND srd.run_id <> :decisionRun)
  AND (EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = :video AND d.status = 'retrying' AND d.retry_run_id = :run)
    OR EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = :video AND e.status = 'retrying' AND e.retry_run_id = :run))`

// PlaylistSendGuard is an immutable descriptor returned only to the winning
// caller of ClaimPlaylistSend. Loading a persisted row never creates a receipt.
type PlaylistSendGuard struct {
	PlaylistSendClaim
	PreparedAt time.Time
}

// ListUnresolvedMutations returns only guards attributed to this run and the
// requested videos. Persisted guards are uncertainty, never proof of an attempt.
func (s *Store) ListUnresolvedMutations(ctx context.Context, runID int64, videoIDs []int64) (map[int64][]UnresolvedMutation, error) {
	out := make(map[int64][]UnresolvedMutation, len(videoIDs))
	if len(videoIDs) == 0 {
		return out, nil
	}
	for _, id := range videoIDs {
		out[id] = []UnresolvedMutation{}
	}
	args := make([]any, 0, len(videoIDs)+1)
	args = append(args, runID)
	marks := make([]string, len(videoIDs))
	for i, id := range videoIDs {
		marks[i] = "?"
		args = append(args, id)
	}
	query := `SELECT video_id, action_type, target_playlist_id, prepared_at FROM playlist_send_guard WHERE run_id=? AND video_id IN (` + strings.Join(marks, ",") + `) ORDER BY video_id, prepared_at, target_playlist_id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing unresolved playlist mutations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var videoID int64
		var item UnresolvedMutation
		var prepared string
		if err := rows.Scan(&videoID, &item.ActionType, &item.TargetPlaylistID, &prepared); err != nil {
			return nil, fmt.Errorf("scanning unresolved playlist mutation: %w", err)
		}
		item.PreparedAt, err = time.Parse(time.RFC3339Nano, prepared)
		if err != nil {
			return nil, fmt.Errorf("parsing unresolved playlist preparation time: %w", err)
		}
		out[videoID] = append(out[videoID], item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating unresolved playlist mutations: %w", err)
	}
	return out, nil
}

type PlaylistClaimResult string

const (
	PlaylistClaimed          PlaylistClaimResult = "claimed"
	PlaylistAlreadyCompleted PlaylistClaimResult = "already_completed"
	PlaylistUncertainBlocked PlaylistClaimResult = "uncertain_blocked"
)

// ClaimPlaylistSend atomically validates the run's captured action authority,
// checks exact-target success, and inserts the replay-veto guard.
func (s *Store) ClaimPlaylistSend(ctx context.Context, c PlaylistSendClaim, now time.Time) (PlaylistClaimResult, *PlaylistSendGuard, error) {
	if c.VideoID <= 0 || c.RunID <= 0 || c.RouteDecisionID <= 0 || c.TargetPlaylistID == "" || c.AuthorizationID == "" || c.ActionType != playlistInsert || c.DecisionRunID < 0 {
		return "", nil, errors.New("state: invalid playlist send claim")
	}
	if c.DecisionRunID == c.RunID {
		c.DecisionRunID = 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, fmt.Errorf("beginning playlist claim: %w", err)
	}
	defer tx.Rollback()
	var runType, status string
	var authVersion, mutationSafetyVersion int
	err = tx.QueryRowContext(ctx, `SELECT run_type, status, action_authority_version, mutation_safety_version FROM run WHERE id = ?`, c.RunID).Scan(&runType, &status, &authVersion, &mutationSafetyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("reading playlist claim run: %w", err)
	}
	if (runType != string(RunTypeManual) && runType != string(RunTypeScheduled)) || status != string(RunStatusRunning) || authVersion != 1 || mutationSafetyVersion != 1 {
		return "", nil, errors.New("state: run is not eligible for playlist mutation")
	}
	var authorized int
	if c.DecisionRunID != 0 {
		err = tx.QueryRowContext(ctx, continuedAuthorityQuery,
			sql.Named("decision", c.RouteDecisionID), sql.Named("decisionRun", c.DecisionRunID),
			sql.Named("video", c.VideoID), sql.Named("target", c.TargetPlaylistID),
			sql.Named("run", c.RunID)).Scan(&authorized)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM route_decision rd
JOIN run r ON r.id=rd.run_id
JOIN config_version cv ON cv.id=r.config_version_id
WHERE rd.id=? AND rd.run_id=? AND rd.video_id=? AND rd.decision='matched'
AND EXISTS (SELECT 1 FROM json_each(cv.snapshot, '$.routes') route,
 json_each(route.value, '$.actions') action
 WHERE CAST(json_extract(route.value,'$.id') AS INTEGER)=rd.route_id
 AND json_extract(action.value,'$.action_type')='playlist_insert'
 AND json_extract(action.value,'$.target_playlist_id')=?)`, c.RouteDecisionID, c.RunID, c.VideoID, c.TargetPlaylistID).Scan(&authorized)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, errors.New("state: playlist claim lacks captured run-owned action authority")
	}
	if err != nil {
		return "", nil, fmt.Errorf("validating playlist claim authority: %w", err)
	}
	var completed int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM executed_action WHERE video_id=? AND target_playlist_id=? AND action_type=? AND outcome='succeeded' LIMIT 1`, c.VideoID, c.TargetPlaylistID, c.ActionType).Scan(&completed)
	if err == nil {
		return PlaylistAlreadyCompleted, nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, fmt.Errorf("checking playlist completion: %w", err)
	}
	// Queue status is deliberately irrelevant: dismissal or ownership changes
	// cannot erase historical uncertainty. A matched version-0 decision whose
	// snapshot/route cannot be read remains unresolved. Otherwise each captured
	// playlist target is resolved only by durable exact-target success; resolving
	// all captured targets permits an unrelated new target.
	var legacyEligible int
	err = tx.QueryRowContext(ctx, legacyClaimVetoQuery, c.VideoID, c.TargetPlaylistID, c.RunID).Scan(&legacyEligible)
	if err == nil {
		return PlaylistUncertainBlocked, nil, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, fmt.Errorf("checking legacy playlist admission: %w", err)
	}
	prepared := now.UTC()
	res, err := tx.ExecContext(ctx, `INSERT INTO playlist_send_guard(video_id,target_playlist_id,action_type,run_id,route_decision_id,authorization_id,prepared_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(video_id,target_playlist_id,action_type) DO NOTHING`, c.VideoID, c.TargetPlaylistID, c.ActionType, c.RunID, c.RouteDecisionID, c.AuthorizationID, formatRunTime(prepared))
	if err != nil {
		return "", nil, fmt.Errorf("inserting playlist guard: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", nil, fmt.Errorf("checking playlist guard insertion: %w", err)
	}
	if n == 0 {
		return PlaylistUncertainBlocked, nil, nil
	}
	if err := tx.Commit(); err != nil {
		return "", nil, fmt.Errorf("committing playlist guard: %w", err)
	}
	return PlaylistClaimed, &PlaylistSendGuard{PlaylistSendClaim: c, PreparedAt: prepared}, nil
}

// ResolvePlaylistSend atomically writes conclusive action evidence and removes
// the exact guard owned by this live receipt. Unknown outcomes are deliberately
// not resolvable by this operation.
func (s *Store) ResolvePlaylistSend(ctx context.Context, g PlaylistSendGuard, outcome ActionOutcome, reason *string, attemptedAt *time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning playlist resolution: %w", err)
	}
	defer tx.Rollback()
	if err := s.ResolvePlaylistSendTx(ctx, tx, g, outcome, reason, attemptedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing playlist resolution: %w", err)
	}
	return nil
}

// ResolvePlaylistSendTx writes a conclusive disposition and removes the exact
// guard owned by g inside tx. The caller owns commit/rollback so retained-work
// handoff can be composed atomically with resolution.
func (s *Store) ResolvePlaylistSendTx(ctx context.Context, tx *sql.Tx, g PlaylistSendGuard, outcome ActionOutcome, reason *string, attemptedAt *time.Time) error {
	if tx == nil {
		return errors.New("state: playlist resolution transaction is required")
	}
	if outcome != ActionOutcomeSucceeded && outcome != ActionOutcomeDeferred && outcome != ActionOutcomeNotAttempted {
		return errors.New("state: playlist guard resolution must be conclusive")
	}
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM run WHERE id=?`, g.RunID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("reading resolution run: %w", err)
	}
	if status != string(RunStatusRunning) {
		return errors.New("state: cannot resolve playlist guard for terminal run")
	}
	if outcome == ActionOutcomeSucceeded && attemptedAt == nil {
		return errors.New("state: successful playlist resolution requires attempt time")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO executed_action(route_decision_id,run_id,video_id,action_type,target_playlist_id,outcome,outcome_reason,executed_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(run_id,video_id,target_playlist_id,action_type) DO UPDATE SET route_decision_id=excluded.route_decision_id,outcome=excluded.outcome,outcome_reason=excluded.outcome_reason,executed_at=excluded.executed_at
		WHERE executed_action.outcome != 'succeeded'`, g.RouteDecisionID, g.RunID, g.VideoID, g.ActionType, g.TargetPlaylistID, outcome, reason, nullableRunTime(attemptedAt))
	if err != nil {
		return fmt.Errorf("recording playlist resolution: %w", err)
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM playlist_send_guard WHERE video_id=? AND target_playlist_id=? AND action_type=? AND run_id=? AND route_decision_id=? AND authorization_id=? AND prepared_at=?`, g.VideoID, g.TargetPlaylistID, g.ActionType, g.RunID, g.RouteDecisionID, g.AuthorizationID, formatRunTime(g.PreparedAt))
	if err != nil {
		return fmt.Errorf("deleting owned playlist guard: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("state: playlist guard receipt is stale or already resolved")
	}
	return nil
}

func nullableRunTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatRunTime(*t)
}
