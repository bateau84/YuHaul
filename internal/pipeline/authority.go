package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/metadata"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// productionAuthority always reads persisted provenance, never caller assertions.
func productionAuthority(ctx context.Context, db *sql.DB, cs ConfigStore, runID int64) (*state.Run, []router.Route, error) {
	s, err := state.NewStore(db)
	if err != nil {
		return nil, nil, err
	}
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}
	if (run.RunType != state.RunTypeManual && run.RunType != state.RunTypeScheduled) ||
		(run.Status != state.RunStatusRunning && run.Status != state.RunStatusInterrupted) ||
		(run.ActionAuthorityVersion != 0 && run.ActionAuthorityVersion != 1) {
		return nil, nil, fmt.Errorf("run %d has no production action authority", runID)
	}
	if cs == nil || run.ConfigVersionID == nil {
		return nil, nil, fmt.Errorf("run %d missing captured configuration", runID)
	}
	cv, err := cs.Get(ctx, *run.ConfigVersionID)
	if err != nil {
		return nil, nil, fmt.Errorf("run %d snapshot: %w", runID, err)
	}
	if err := validateSnapshotRoutes(cv.Snapshot); err != nil {
		return nil, nil, fmt.Errorf("run %d snapshot: %w", runID, err)
	}
	routes := routesFromSnapshot(cv.Snapshot)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].EvaluationOrder < routes[j].EvaluationOrder })
	seen := map[int64]bool{}
	for i := range routes {
		r := &routes[i]
		if r.ID <= 0 || seen[r.ID] {
			return nil, nil, fmt.Errorf("run %d invalid snapshot route %d", runID, r.ID)
		}
		seen[r.ID] = true
		sort.SliceStable(r.Conditions, func(i, j int) bool { return r.Conditions[i].ConditionIndex < r.Conditions[j].ConditionIndex })
		sort.SliceStable(r.Actions, func(i, j int) bool { return r.Actions[i].ActionIndex < r.Actions[j].ActionIndex })
	}
	return run, routes, nil
}

func (p *Pipeline) productionRoutes(ctx context.Context, runID int64) ([]router.Route, error) {
	_, routes, err := productionAuthority(ctx, p.db, p.configStore, runID)
	return routes, err
}

type authorityError struct{ error }

// admitLegacy is read-only, including metadata access. Missing input is not a
// negative predicate. Native evidence deliberately does not reread metadata.
func admitLegacy(ctx context.Context, db *sql.DB, run *state.Run, videoID int64, routes []router.Route, complete bool) error {
	_, err := checkedLegacyEvaluation(ctx, db, run, videoID, routes, complete)
	return err
}

func checkedLegacyEvaluation(ctx context.Context, db *sql.DB, run *state.Run, videoID int64, routes []router.Route, complete bool) (_ []router.ExpectedEvaluation, err error) {
	defer func() {
		if err != nil {
			err = &authorityError{err}
		}
	}()
	if run.ActionAuthorityVersion == 1 {
		return nil, nil
	}
	s, err := state.NewStore(db)
	if err != nil {
		return nil, err
	}
	v, err := s.GetVideo(ctx, videoID)
	if err != nil {
		return nil, err
	}
	m, metaErr := metadata.NewStore(db).Get(ctx, v.VideoID)
	available := metaErr == nil && m.ExpiredAt == nil && m.ExpiresAt.After(time.Now().UTC())
	if !available {
		m = &metadata.TransientMetadata{}
	}
	need := func(field string) error {
		ok := false
		switch field {
		case "channel_id":
			ok = v.ChannelID != ""
		case "title":
			ok = available && m.Title != nil
		case "channel_name":
			ok = available && m.ChannelName != nil
		case "description":
			ok = available && m.Description != nil
		case "duration_seconds":
			ok = available && m.DurationSeconds != nil
		}
		if !ok {
			return fmt.Errorf("legacy run %d video %d snapshot input unavailable: %s", run.ID, videoID, field)
		}
		return nil
	}
	for _, r := range routes {
		if !r.IsEnabled {
			continue
		}
		for _, ex := range r.Exclusions {
			var field string
			switch ex.ExclusionType {
			case router.ExclusionTypeChannel, router.ExclusionTypeVideo:
				continue
			case router.ExclusionTypeTitle, router.ExclusionTypeWord:
				field = "title"
			case router.ExclusionTypeDurationMin, router.ExclusionTypeDurationMax:
				field = "duration_seconds"
			default:
				return nil, fmt.Errorf("unsupported snapshot exclusion for route %d", r.ID)
			}
			if err := need(field); err != nil {
				return nil, err
			}
		}
	}
	expected := router.NewEngine(nil).ExpectedEvaluations(run.ID, videoID, videoContextFromMetadata(v.VideoID, v.ChannelID, m), routes)
	byRoute := map[int64]router.ExpectedEvaluation{}
	for _, e := range expected {
		r := findRoute(routes, e.Decision.RouteID)
		if len(e.Audits) > 0 && r.ConditionComposition != "AND" && r.ConditionComposition != "OR" {
			return nil, fmt.Errorf("legacy route %d unsupported snapshot composition", e.Decision.RouteID)
		}
		seenConditions := map[int]bool{}
		for _, a := range e.Audits {
			if seenConditions[a.ConditionIndex] {
				return nil, fmt.Errorf("legacy route %d duplicate snapshot condition index %d", e.Decision.RouteID, a.ConditionIndex)
			}
			seenConditions[a.ConditionIndex] = true
			switch router.ConditionOperator(a.Operator) {
			case router.ConditionOperatorContains, router.ConditionOperatorEquals, router.ConditionOperatorStartsWith, router.ConditionOperatorEndsWith, router.ConditionOperatorMatchesRegex, router.ConditionOperatorGreaterThan, router.ConditionOperatorLessThan, router.ConditionOperatorBetween:
			default:
				return nil, fmt.Errorf("legacy route %d unsupported snapshot operator", e.Decision.RouteID)
			}
			if err := need(a.Field); err != nil {
				return nil, err
			}
		}
		byRoute[e.Decision.RouteID] = e
	}
	actual, err := s.GetRouteDecisionsForVideoInRun(ctx, videoID, run.ID)
	if err != nil {
		return nil, err
	}
	if complete && len(actual) != len(expected) {
		return nil, fmt.Errorf("legacy run %d video %d incomplete snapshot evaluation", run.ID, videoID)
	}
	for _, d := range actual {
		e, ok := byRoute[d.RouteID]
		want := e.Decision
		want.ID = d.ID
		// Legacy generic exclusion rows predate causal references. Compare the
		// original evaluation evidence independently; never enrich the stored row.
		actualBase, wantBase := d, want
		actualBase.ExclusionIndex, actualBase.ExclusionSemanticsVersion = nil, nil
		wantBase.ExclusionIndex, wantBase.ExclusionSemanticsVersion = nil, nil
		refMatches := d.ExclusionIndex == nil && d.ExclusionSemanticsVersion == nil
		if d.ExclusionIndex != nil && d.ExclusionSemanticsVersion != nil {
			refMatches = false
			r := findRoute(routes, d.RouteID)
			if r != nil && *d.ExclusionIndex >= 0 && *d.ExclusionIndex < len(r.Exclusions) &&
				*d.ExclusionSemanticsVersion == 1 && d.Decision == state.RouteDecisionSkipped &&
				d.SkipReason != nil && *d.SkipReason == state.SkipReasonExclusion {
				ex := r.Exclusions[*d.ExclusionIndex]
				switch ex.ExclusionType {
				case router.ExclusionTypeChannel, router.ExclusionTypeVideo, router.ExclusionTypeTitle, router.ExclusionTypeWord:
					refMatches = true
				case router.ExclusionTypeDurationMin, router.ExclusionTypeDurationMax:
					_, err := strconv.Atoi(ex.ExclusionValue)
					refMatches = err == nil
				}
			}
		}
		if !ok || !reflect.DeepEqual(actualBase, wantBase) || !refMatches {
			return nil, fmt.Errorf("legacy run %d video %d decision %d route %d snapshot mismatch", run.ID, videoID, d.ID, d.RouteID)
		}
		audits, err := s.GetConditionAuditsForDecision(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		if len(audits) != len(e.Audits) {
			return nil, fmt.Errorf("legacy decision %d route %d snapshot audit count mismatch", d.ID, d.RouteID)
		}
		for i, a := range audits {
			want := e.Audits[i]
			want.ID = a.ID
			want.RouteDecisionID = d.ID
			if !reflect.DeepEqual(a, want) {
				return nil, fmt.Errorf("legacy decision %d route %d snapshot audit mismatch", d.ID, d.RouteID)
			}
		}
	}
	return expected, nil
}

func (p *Pipeline) completeLegacyEvaluation(ctx context.Context, run *state.Run, v *state.Video, routes []router.Route) (*router.EvaluationResult, error) {
	if err := p.ValidateRunRoutes(ctx, run.ID, routes); err != nil {
		return nil, fmt.Errorf("validate legacy evaluation routes: %w", err)
	}
	expected, err := checkedLegacyEvaluation(ctx, p.db, run, v.ID, routes, false)
	if err != nil {
		return nil, err
	}
	result := &router.EvaluationResult{}
	for _, e := range expected {
		if _, err := p.machine.RecordRouteEvaluation(ctx, &e.Decision, e.Audits); err != nil {
			return nil, err
		}
		if e.Decision.Decision == state.RouteDecisionMatched {
			result.MatchedRoutes = append(result.MatchedRoutes, e.Decision.RouteID)
		}
	}
	if err := admitLegacy(ctx, p.db, run, v.ID, routes, true); err != nil {
		return nil, err
	}
	return result, nil
}

// Retain all unfinished candidates before marking an authority failure
// terminal. If the atomic handoff cannot complete, leave the run resumable.
func (p *Pipeline) retainAuthorityFailure(ctx context.Context, runID int64, videos []*state.Video, cause error) {
	// Persisted reasons, queue detail, and SSE carry only the vetted bounded
	// summary; the full chain stays in the server log (ARD-001).
	reason := diag.Reason(cause, "snapshot action authority failure")
	p.retainThenFail(ctx, runID, reason, func(retainCtx context.Context, qs *queue.Store, tx *sql.Tx) error {
		for _, v := range videos {
			if v.ProcessingState == state.VideoStateCompleted {
				continue
			}
			if _, err := qs.RetainRuntimeErrorTx(retainCtx, tx, v.ID, runID, "snapshot action authority failure", reason); err != nil {
				return err
			}
			if err := qs.AcknowledgeRetryGroupTx(retainCtx, tx, v.ID, runID); err != nil {
				return err
			}
		}
		return nil
	})
}

// retainThenFail is the one place that orders retention before terminal
// failure (ADR-002, ADR-006 §3). retain runs in a single writer transaction,
// once under ctx and once more under a detached bounded context. Only after it
// commits is the run marked failed. If retention or the failed transition
// cannot complete, the run is left resumable as interrupted and is never
// failed. It reports whether the run became failed.
func (p *Pipeline) retainThenFail(ctx context.Context, runID int64, reason string, retain func(context.Context, *queue.Store, *sql.Tx) error) bool {
	qs, err := queue.NewStore(p.db)
	if err != nil {
		p.logger.ErrorContext(ctx, "open run retention store", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "retain_failure"))
		p.interruptAuthorityRun(ctx, runID, reason)
		return false
	}
	attempt := func(retainCtx context.Context) error {
		return qs.WithRetryTx(retainCtx, func(tx *sql.Tx) error { return retain(retainCtx, qs, tx) })
	}
	if err = attempt(ctx); err != nil {
		p.logger.ErrorContext(ctx, "retain run failure", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "retain_failure"))
		// Retention is the handoff. Retry it independently of the possibly
		// expired execution context before choosing a terminal status.
		finalizeCtx, cancel := runFinalizationContext(ctx)
		err = attempt(finalizeCtx)
		cancel()
		if err != nil {
			p.logger.ErrorContext(ctx, "run retention finalization failed", "run_id", runID, "reason", reason, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
			// The candidates remain owned by the run, so interruption is
			// resumable. Persist it separately; never fail before queue handoff.
			p.interruptAuthorityRun(ctx, runID, reason)
			return false
		}
	}
	finalizeCtx, cancel := runFinalizationContext(ctx)
	_, err = p.machine.FailRun(finalizeCtx, runID, reason)
	cancel()
	if err != nil {
		p.logger.ErrorContext(ctx, "fail retained run", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
		p.interruptAuthorityRun(ctx, runID, reason)
		return false
	}
	p.broadcastEvent(runID, EventRunError, payload(errorPayload{Message: reason}))
	return true
}

// sweepStrandedWork hands every video this production run has not finished to
// a retained queue (ADR-006 §3). It makes no provider call, touches no send
// guard and writes no action evidence or counters.
func sweepStrandedWork(ctx context.Context, qs *queue.Store, tx *sql.Tx, runID int64, reason string) error {
	var runType string
	if err := tx.QueryRowContext(ctx, `SELECT run_type FROM run WHERE id=?`, runID).Scan(&runType); err != nil {
		return fmt.Errorf("sweep run %d: %w", runID, err)
	}
	if runType != string(state.RunTypeManual) && runType != string(state.RunTypeScheduled) {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT v.id,
	  EXISTS(SELECT 1 FROM deferred_item d WHERE d.video_id=v.id AND d.retry_run_id=?1)
	   OR EXISTS(SELECT 1 FROM error_entry e WHERE e.video_id=v.id AND e.retry_run_id=?1)
	 FROM video v
	 WHERE (v.discovered_in_run_id=?1
	   OR v.id IN (SELECT video_id FROM route_decision WHERE run_id=?1)
	   OR v.id IN (SELECT video_id FROM deferred_item WHERE retry_run_id=?1)
	   OR v.id IN (SELECT video_id FROM error_entry WHERE retry_run_id=?1))
	  AND v.processing_state <> 'completed'
	  AND NOT EXISTS(SELECT 1 FROM run_video_outcome o WHERE o.run_id=?1 AND o.video_id=v.id
	   AND o.final_outcome IN ('accepted','rejected','excluded','deferred','failed'))
	 ORDER BY v.id`, runID)
	if err != nil {
		return fmt.Errorf("select unfinished videos: %w", err)
	}
	type member struct {
		videoID    int64
		retryOwned bool
	}
	var unfinished []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.videoID, &m.retryOwned); err != nil {
			rows.Close()
			return fmt.Errorf("scan unfinished video: %w", err)
		}
		unfinished = append(unfinished, m)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, m := range unfinished {
		if m.retryOwned {
			// Queue work this run claimed returns to its queue with its IDs,
			// origin run, reasons and history. A group the operator removed
			// stays removed; a requeued group is already retained.
			if err := qs.RequeueRetryGroupTx(ctx, tx, m.videoID, runID); err != nil {
				return err
			}
			continue
		}
		if _, err := qs.RetainRunSinkTx(ctx, tx, m.videoID, runID, queue.ErrorCategoryRuntime, "run stopped before this video finished", reason); err != nil {
			return err
		}
	}
	return nil
}

func (p *Pipeline) interruptAuthorityRun(ctx context.Context, runID int64, reason string) {
	p.interruptRunWithCause(ctx, runID, reason, "")
}

// interruptRunWithCause persists an interruption under the bounded finalization
// context. A non-empty cause is stored with the status in the same write; an
// empty cause means the cause is not proven and is stored as NULL.
func (p *Pipeline) interruptRunWithCause(ctx context.Context, runID int64, reason, cause string) {
	finalizeCtx, cancel := runFinalizationContext(ctx)
	defer cancel()
	var err error
	if cause == "" {
		err = p.stateStore.InterruptRun(finalizeCtx, runID, reason)
	} else {
		err = p.stateStore.InterruptRunWithCause(finalizeCtx, runID, reason, cause)
	}
	if err != nil {
		p.logger.ErrorContext(ctx, "interrupt authority run finalization failed", "run_id", runID, "reason", reason, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
	}
}
