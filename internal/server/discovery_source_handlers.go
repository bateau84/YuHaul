package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/state"
)

// Read-only projections of the durable uploads-source failure record
// (discovery ARS-002 section 4). There is no write method: a source entry has
// no Retry, Dismiss or suppression, and a POST under /api/errors/sources is
// refused by the video error queue's integer ID parsing.

const (
	defaultSourceFailureLimit = 50
	maxSourceFailureLimit     = 200
)

// parseSourceFailureQuery reads limit (1..200, default 50) and before_id
// (positive). It writes the 400 itself and reports false on a bad value.
func parseSourceFailureQuery(w http.ResponseWriter, r *http.Request) (state.SourceFailureQuery, bool) {
	q := state.SourceFailureQuery{Limit: defaultSourceFailureLimit}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxSourceFailureLimit {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return q, false
		}
		q.Limit = n
	}
	if v := r.URL.Query().Get("before_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid before_id")
			return q, false
		}
		q.BeforeID = n
	}
	return q, true
}

// handleListSourceFailures serves GET /api/errors/sources: permanent failures
// across runs, newest first.
func (s *Server) handleListSourceFailures(w http.ResponseWriter, r *http.Request) {
	q, ok := parseSourceFailureQuery(w, r)
	if !ok {
		return
	}
	q.PermanentOnly = true
	s.writeSourceFailures(w, r, q, 0)
}

// handleListRunSourceFailures serves GET /api/runs/{id}/discovery-source-errors:
// the run's failures of every category, newest first.
func (s *Server) handleListRunSourceFailures(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || runID < 1 {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	q, ok := parseSourceFailureQuery(w, r)
	if !ok {
		return
	}
	if _, err := s.runVideoStore.GetRun(r.Context(), runID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		if s.clientAborted(r.Context(), "get run failed", err) {
			return
		}
		s.logger.ErrorContext(r.Context(), "get run failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	q.RunID = runID
	s.writeSourceFailures(w, r, q, runID)
}

func (s *Server) writeSourceFailures(w http.ResponseWriter, r *http.Request, q state.SourceFailureQuery, runID int64) {
	page, err := s.runVideoStore.ListSourceFailures(r.Context(), q)
	if err != nil {
		if s.clientAborted(r.Context(), "list source failures failed", err) {
			return
		}
		attrs := []any{"diagnostic", diag.Encode(err, "unknown", "inspect_run")}
		if runID != 0 {
			attrs = append([]any{"run_id", runID}, attrs...)
		}
		s.logger.ErrorContext(r.Context(), "list source failures failed", attrs...)
		writeError(w, http.StatusInternalServerError, "failed to list channel scan errors")
		return
	}
	if page.Withheld > 0 {
		s.logger.WarnContext(r.Context(), "source failure rows withheld", "count", page.Withheld)
	}
	writeJSON(w, http.StatusOK, page)
}
