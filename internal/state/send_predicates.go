package state

// Shared SQL predicates of ARS-005 §2. Each is SQL text that refers only to
// the aliases named below. Callers supply those aliases and never edit the
// text, so the real send path (ClaimPlaylistSend) and the stranded scan
// evaluate one definition.

// LegacySendVeto is the boolean condition under which a version-0 production
// run keeps an unresolved claim on a video, so a new send must be refused.
//
// Aliases the caller supplies:
//   - old_run: a run row;
//   - subject: columns video_id, target_playlist_id (may be NULL),
//     receiving_run_id and assume_acted (0 or 1).
//
// assume_acted = 1 evaluates branch 3 as it will be at send time (the
// receiving run sets the video to 'acted' before it sends). With
// assume_acted = 0 the condition is the original ClaimPlaylistSend text.
// A NULL target makes branch 4 treat the target as unresolved.
//
// Only unresolved outcomes veto in branch 4. 'unavailable_on_youtube' is
// final (like accepted/rejected/excluded/deferred), so it does not veto.
const LegacySendVeto = `old_run.id <> subject.receiving_run_id
AND old_run.run_type IN ('manual','scheduled')
AND old_run.mutation_safety_version = 0
AND (
  EXISTS (
    SELECT 1 FROM route_decision old_decision
    LEFT JOIN config_version old_config ON old_config.id = old_run.config_version_id
    WHERE old_decision.run_id = old_run.id AND old_decision.video_id = subject.video_id
      AND old_decision.decision = 'matched'
      AND (
        old_config.id IS NULL
        OR NOT EXISTS (
          SELECT 1 FROM json_each(old_config.snapshot, '$.routes') route
          WHERE CAST(json_extract(route.value, '$.id') AS INTEGER) = old_decision.route_id)
        OR EXISTS (
          SELECT 1 FROM json_each(old_config.snapshot, '$.routes') route,
                        json_each(route.value, '$.actions') action
          WHERE CAST(json_extract(route.value, '$.id') AS INTEGER) = old_decision.route_id
            AND json_extract(action.value, '$.action_type') = 'playlist_insert'
            AND NOT EXISTS (
              SELECT 1 FROM executed_action resolved
              WHERE resolved.video_id = old_decision.video_id
                AND resolved.target_playlist_id = json_extract(action.value, '$.target_playlist_id')
                AND resolved.action_type = 'playlist_insert' AND resolved.outcome = 'succeeded'))))
  OR EXISTS (
    SELECT 1 FROM executed_action old_action
    WHERE old_action.run_id = old_run.id AND old_action.video_id = subject.video_id
      AND old_action.action_type = 'playlist_insert'
      AND (old_action.target_playlist_id IS NULL OR NOT EXISTS (
        SELECT 1 FROM executed_action resolved
        WHERE resolved.video_id = old_action.video_id
          AND resolved.target_playlist_id = old_action.target_playlist_id
          AND resolved.action_type = 'playlist_insert' AND resolved.outcome = 'succeeded')))
  OR EXISTS (
    SELECT 1 FROM video old_video
    WHERE old_video.id = subject.video_id
      AND (subject.assume_acted = 1 OR old_video.processing_state = 'acted')
      AND old_video.discovered_in_run_id = old_run.id
      AND old_run.current_phase = 'action_execution'
      AND NOT EXISTS (
        SELECT 1 FROM route_decision recorded
        WHERE recorded.run_id = old_run.id AND recorded.video_id = old_video.id
          AND recorded.decision = 'matched'))
  OR (
    old_run.current_phase = 'action_execution'
    AND EXISTS (
      SELECT 1 FROM run_video_outcome retry_outcome
      WHERE retry_outcome.run_id = old_run.id AND retry_outcome.video_id = subject.video_id
        AND (retry_outcome.final_outcome IS NULL
             OR retry_outcome.final_outcome IN ('not_yet_evaluated','failed')))
    AND (
      EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = subject.video_id AND d.retry_run_id = old_run.id)
      OR EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = subject.video_id AND e.retry_run_id = old_run.id))
    AND (subject.target_playlist_id IS NULL OR NOT EXISTS (
      SELECT 1 FROM executed_action resolved
      WHERE resolved.video_id = subject.video_id
        AND resolved.target_playlist_id = subject.target_playlist_id
        AND resolved.action_type = 'playlist_insert' AND resolved.outcome = 'succeeded')))
)`

// HeldSendGuard is a row source of held playlist_send_guard rows for
// subject.video_id. A guard blocks only while its target has no recorded
// success; `uncertain` is true when the guard's own run recorded
// mutation_application_uncertain for that target.
const HeldSendGuard = `SELECT g.video_id, g.run_id, g.target_playlist_id,
  EXISTS (SELECT 1 FROM executed_action u
          WHERE u.run_id = g.run_id AND u.video_id = g.video_id
            AND u.target_playlist_id = g.target_playlist_id AND u.action_type = 'playlist_insert'
            AND u.outcome = 'failed' AND u.outcome_reason = 'mutation_application_uncertain') AS uncertain
FROM playlist_send_guard g
WHERE g.video_id = subject.video_id AND g.action_type = 'playlist_insert'
  AND NOT EXISTS (SELECT 1 FROM executed_action s
                  WHERE s.video_id = g.video_id AND s.target_playlist_id = g.target_playlist_id
                    AND s.action_type = 'playlist_insert' AND s.outcome = 'succeeded')`

// legacyClaimVetoQuery is ClaimPlaylistSend's legacy check: the shared veto
// with assume_acted = 0. Parameters: video id, target playlist id, run id.
const legacyClaimVetoQuery = `SELECT 1
FROM (SELECT ?1 AS video_id, ?2 AS target_playlist_id, ?3 AS receiving_run_id, 0 AS assume_acted) subject
JOIN run old_run ON ` + LegacySendVeto + `
LIMIT 1`
