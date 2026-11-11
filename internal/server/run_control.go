package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/state"
)

// parseRunID reads the {id} path value: a positive integer.
func parseRunID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// handleStopRun is POST /api/runs/{id}/stop (run-initiation ARS-003 §4.1). The
// stop is acknowledged once it is recorded and the run's context is cancelled;
// the run reads "stopping" until it settles as "stopped".
func (s *Server) handleStopRun(w http.ResponseWriter, r *http.Request) {
	id, ok := parseRunID(r)
	if !ok {
		writeErrorCode(w, http.StatusBadRequest, "invalid_run_id", "invalid run id", nil)
		return
	}
	if !s.trackRun() {
		writeErrorCode(w, http.StatusServiceUnavailable, "shutting_down", "server is shutting down", map[string]any{"run_id": id})
		return
	}
	res, err := s.pipeline.StopRun(r.Context(), id)
	s.runWG.Done()
	if err != nil {
		var refused *pipeline.StopRefusedError
		switch {
		case errors.As(err, &refused):
			switch refused.Code {
			case pipeline.StopRunNotFound:
				writeErrorCode(w, http.StatusNotFound, "run_not_found", fmt.Sprintf("run %d does not exist", id), map[string]any{"run_id": id})
			case pipeline.StopRunEnded:
				writeErrorCode(w, http.StatusConflict, "run_ended", fmt.Sprintf("run %d has already ended", id), map[string]any{"run_id": id, "status": refused.Status})
			default:
				writeErrorCode(w, http.StatusConflict, "run_not_active", fmt.Sprintf("run %d is not running", id), map[string]any{"run_id": id, "reason": refused.Reason})
			}
		default:
			s.logger.ErrorContext(r.Context(), "stop run failed", "run_id", id, "diagnostic", diag.Encode(err, "unknown", "finalize_run"))
			writeErrorCode(w, http.StatusInternalServerError, "stop_failed", fmt.Sprintf("the stop was not recorded; run %d continues", id), map[string]any{"run_id": id})
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": id, "status": string(state.RunStatusRunning), "stop": res.Outcome})
}

// handleDiscardRun is POST /api/runs/{id}/discard (ARS-002 §3). It runs
// synchronously in the request.
func (s *Server) handleDiscardRun(w http.ResponseWriter, r *http.Request) {
	id, ok := parseRunID(r)
	if !ok {
		writeErrorCode(w, http.StatusBadRequest, "invalid_run_id", "invalid run id", nil)
		return
	}
	if !s.trackRun() {
		writeErrorCode(w, http.StatusServiceUnavailable, "shutting_down", "server is shutting down", map[string]any{"run_id": id})
		return
	}
	res, err := s.pipeline.DiscardRun(r.Context(), id)
	s.runWG.Done()
	if err != nil {
		var refused *pipeline.DiscardRefusedError
		switch {
		case errors.Is(err, pipeline.ErrStartupRecoveryActive):
			writeErrorCode(w, http.StatusServiceUnavailable, "startup_recovery_active", pipeline.ErrStartupRecoveryActive.Error(), map[string]any{"run_id": id})
		case errors.Is(err, pipeline.ErrProductionRunActive):
			writeErrorCode(w, http.StatusConflict, "production_run_active", pipeline.ErrProductionRunActive.Error(), map[string]any{"run_id": id})
		case errors.As(err, &refused):
			switch refused.Code {
			case pipeline.DiscardRunNotFound:
				writeErrorCode(w, http.StatusNotFound, "run_not_found", fmt.Sprintf("run %d does not exist", id), map[string]any{"run_id": id})
			case pipeline.DiscardRunEnded:
				writeErrorCode(w, http.StatusConflict, "run_ended", fmt.Sprintf("run %d has already ended", id), map[string]any{"run_id": id, "status": refused.Status})
			default:
				writeErrorCode(w, http.StatusConflict, "run_not_discardable", fmt.Sprintf("run %d is not the run that Run Now cannot resume", id), map[string]any{"run_id": id, "reason": refused.Reason})
			}
		case errors.Is(err, pipeline.ErrDiscardOutcomeUnknown):
			writeErrorCode(w, http.StatusInternalServerError, "discard_outcome_unknown", fmt.Sprintf("could not confirm whether run %d was discarded; reload the run", id), map[string]any{"run_id": id})
		default:
			writeErrorCode(w, http.StatusInternalServerError, "discard_failed", fmt.Sprintf("the discard did not take effect; run %d is unchanged", id), map[string]any{"run_id": id})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": res.RunID, "status": string(state.RunStatusFailed), "outcome": "discarded", "cause": res.Cause})
}

// handleEndRun is POST /api/runs/{id}/end (run-initiation ARS-004 §3). It runs
// synchronously in the request. The permanent refusals are answered from a
// lock-free precheck so a repeat always says the run has already ended.
func (s *Server) handleEndRun(w http.ResponseWriter, r *http.Request) {
	id, ok := parseRunID(r)
	if !ok {
		writeErrorCode(w, http.StatusBadRequest, "invalid_run_id", "invalid run id", nil)
		return
	}
	if err := s.pipeline.PrecheckEndLegacyRun(r.Context(), id); err != nil {
		s.writeEndRefusal(w, id, err)
		return
	}
	if !s.trackRun() {
		writeErrorCode(w, http.StatusServiceUnavailable, "shutting_down", "server is shutting down", map[string]any{"run_id": id})
		return
	}
	res, err := s.pipeline.EndLegacyRun(r.Context(), id)
	s.runWG.Done()
	if err != nil {
		s.writeEndRefusal(w, id, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run_id": res.RunID, "status": string(state.RunStatusFailed), "outcome": "ended"})
}

func (s *Server) writeEndRefusal(w http.ResponseWriter, id int64, err error) {
	var refused *pipeline.EndRefusedError
	var notApplied *pipeline.EndNotAppliedError
	switch {
	case errors.Is(err, pipeline.ErrStartupRecoveryActive):
		writeErrorCode(w, http.StatusServiceUnavailable, "startup_recovery_active", pipeline.ErrStartupRecoveryActive.Error(), map[string]any{"run_id": id})
	case errors.Is(err, pipeline.ErrProductionRunActive):
		writeErrorCode(w, http.StatusConflict, "production_run_active", pipeline.ErrProductionRunActive.Error(), map[string]any{"run_id": id})
	case errors.As(err, &refused):
		switch refused.Code {
		case pipeline.EndRunNotFound:
			writeErrorCode(w, http.StatusNotFound, "run_not_found", fmt.Sprintf("run %d does not exist", id), map[string]any{"run_id": id})
		case pipeline.EndRunEnded:
			writeErrorCode(w, http.StatusConflict, "run_ended", fmt.Sprintf("run %d has already ended", id), map[string]any{"run_id": id, "status": refused.Status})
		default:
			extra := map[string]any{"run_id": id, "reason": refused.Reason}
			if refused.Reason == pipeline.EndReasonNotProduction {
				extra["run_type"] = refused.RunType
			}
			writeErrorCode(w, http.StatusConflict, "run_not_endable", fmt.Sprintf("run %d cannot be marked as ended", id), extra)
		}
	case errors.Is(err, pipeline.ErrEndOutcomeUnknown):
		writeErrorCode(w, http.StatusInternalServerError, "end_outcome_unknown", fmt.Sprintf("could not confirm whether run %d was marked as ended; reload the run", id), map[string]any{"run_id": id})
	default:
		extra := map[string]any{"run_id": id}
		if errors.As(err, &notApplied) && notApplied.Status != "" {
			extra["status"] = notApplied.Status
		}
		writeErrorCode(w, http.StatusInternalServerError, "end_failed", fmt.Sprintf("the end did not take effect; run %d is unchanged", id), extra)
	}
}

// runReadView holds the facts every run read projects, computed once per
// request: the run Discard would accept, the runs End would accept, and the
// runs this process is executing.
type runReadView struct {
	discardID    int64
	discardCause string
	discardOK    bool
	discardKnown bool
	endable      map[int64]bool
	endableKnown bool // false when the read failed: every item reads null
	executing    map[int64]bool
}

func (s *Server) readView(ctx context.Context) runReadView {
	v := runReadView{executing: s.pipeline.ExecutingRuns()}
	id, cause, ok, err := s.pipeline.DiscardableRun(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "discardable run read failed", "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
	} else {
		v.discardID, v.discardCause, v.discardOK, v.discardKnown = id, cause, ok, true
	}
	endable, err := s.pipeline.EndableRuns(ctx)
	switch {
	case err != nil:
		s.logger.ErrorContext(ctx, "endable runs read failed", "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
	case s.shuttingDown():
		v.endableKnown = true
	default:
		v.endable, v.endableKnown = endable, true
	}
	return v
}

// projectRun adds discardable, discard_cause, endable and executing to a run
// read (ARS-003 §4.2, ARS-004 §4). executing is the in-process run control: a
// saved status of running does not imply it.
func (s *Server) projectRun(ctx context.Context, run state.Run, v runReadView) any {
	raw, err := json.Marshal(run)
	if err != nil {
		return run
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return run
	}
	switch {
	case !v.discardKnown:
		out["discardable"] = json.RawMessage("null")
	case v.discardOK && v.discardID == run.ID:
		out["discardable"] = json.RawMessage("true")
		cause, _ := json.Marshal(v.discardCause)
		out["discard_cause"] = cause
	default:
		out["discardable"] = json.RawMessage("false")
	}
	switch {
	case !v.endableKnown:
		out["endable"] = json.RawMessage("null")
	case v.endable[run.ID]:
		out["endable"] = json.RawMessage("true")
	default:
		out["endable"] = json.RawMessage("false")
	}
	if v.executing[run.ID] {
		out["executing"] = json.RawMessage("true")
	} else {
		out["executing"] = json.RawMessage("false")
	}
	return out
}

// projectOverview carries the same projection on the overview's last_run.
func (s *Server) projectOverview(ctx context.Context, o *Overview) any {
	if o == nil || o.LastRun == nil {
		return o
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return o
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return o
	}
	lr, err := json.Marshal(s.projectRun(ctx, *o.LastRun, s.readView(ctx)))
	if err != nil {
		return o
	}
	out["last_run"] = lr
	return out
}
