package queue

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
)

// Handler returns an http.Handler that serves the error queue endpoints.
// Mount this handler at /api/errors/.
func (s *Service) ErrorHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleListErrors)
	mux.HandleFunc("POST /{id}/retry", s.handleRetryError)
	mux.HandleFunc("POST /{id}/dismiss", s.handleDismissError)
	return mux
}

// DeferredHandler returns an http.Handler that serves the deferred work queue
// endpoints. Mount this handler at /api/deferred/.
func (s *Service) DeferredHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleListDeferred)
	mux.HandleFunc("POST /{id}/retry", s.handleRetryDeferred)
	mux.HandleFunc("POST /{id}/discard", s.handleDiscardDeferred)
	return mux
}

func (s *Service) handleListErrors(w http.ResponseWriter, r *http.Request) {
	include := r.URL.Query().Get("include")
	var entries []ErrorEntry
	var err error

	switch include {
	case "", "active":
		entries, err = s.ListActiveErrors(r.Context())
	case "all":
		entries, err = s.ListAllErrors(r.Context())
	default:
		writeError(w, http.StatusBadRequest, "invalid include value; use 'active' or 'all'")
		return
	}

	if err != nil {
		slog.ErrorContext(r.Context(), "list errors failed", "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to list error queue")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"errors": publicErrorEntries(entries)})
}

func (s *Service) handleRetryError(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	entry, err := s.RetryError(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStartupRecoveryActive) {
			writeError(w, http.StatusServiceUnavailable, ErrStartupRecoveryActive.Error())
			return
		}
		if errors.Is(err, ErrShuttingDown) {
			writeError(w, http.StatusServiceUnavailable, ErrShuttingDown.Error())
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "error entry not found")
			return
		}
		if errors.Is(err, ErrInvalidState) {
			writeError(w, http.StatusConflict, queueFailureMessage(err, "retry"))
			return
		}
		slog.ErrorContext(r.Context(), "retry error failed", "error_entry_id", id, "diagnostic", diag.Encode(err, "unknown", "retry"))
		writeError(w, http.StatusInternalServerError, "failed to retry error entry")
		return
	}

	writeJSON(w, http.StatusOK, publicErrorEntry(entry))
}

func (s *Service) handleDismissError(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	entry, err := s.DismissError(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "error entry not found")
			return
		}
		slog.ErrorContext(r.Context(), "dismiss error failed", "error_entry_id", id, "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to dismiss error entry")
		return
	}

	writeJSON(w, http.StatusOK, publicErrorEntry(entry))
}

func (s *Service) handleListDeferred(w http.ResponseWriter, r *http.Request) {
	include := r.URL.Query().Get("include")
	var items []DeferredItem
	var err error

	switch include {
	case "", "pending":
		items, err = s.ListPendingDeferred(r.Context())
	case "all":
		items, err = s.ListAllDeferred(r.Context())
	default:
		writeError(w, http.StatusBadRequest, "invalid include value; use 'pending' or 'all'")
		return
	}

	if err != nil {
		slog.ErrorContext(r.Context(), "list deferred failed", "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to list deferred queue")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deferred": publicDeferredItems(items)})
}

func (s *Service) handleRetryDeferred(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	item, err := s.RetryDeferred(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStartupRecoveryActive) {
			writeError(w, http.StatusServiceUnavailable, ErrStartupRecoveryActive.Error())
			return
		}
		if errors.Is(err, ErrShuttingDown) {
			writeError(w, http.StatusServiceUnavailable, ErrShuttingDown.Error())
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deferred item not found")
			return
		}
		if errors.Is(err, ErrInvalidState) {
			writeError(w, http.StatusConflict, queueFailureMessage(err, "retry"))
			return
		}
		slog.ErrorContext(r.Context(), "retry deferred failed", "deferred_item_id", id, "diagnostic", diag.Encode(err, "unknown", "retry"))
		writeError(w, http.StatusInternalServerError, "failed to retry deferred item")
		return
	}

	writeJSON(w, http.StatusOK, publicDeferredItem(item))
}

func (s *Service) handleDiscardDeferred(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	item, err := s.DiscardDeferred(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deferred item not found")
			return
		}
		slog.ErrorContext(r.Context(), "discard deferred failed", "deferred_item_id", id, "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to discard deferred item")
		return
	}

	writeJSON(w, http.StatusOK, publicDeferredItem(item))
}

func parseIDParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v := r.PathValue(name)
	if strings.TrimSpace(v) == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// errorEntryView is the public projection of an error queue entry. It keeps
// the frontend's snake_case wire shape and admits only canonical or exact
// legacy diagnostic text. Rejected prose is withheld; unknown_error summaries
// remain useful but set ReasonUnavailable. The stored row and retry/resolution
// lifecycle are unchanged (ARD-001).
type errorEntryView struct {
	RetryRunID        *int64        `json:"retry_run_id,omitempty"`
	ID                int64         `json:"id"`
	VideoID           int64         `json:"video_id"`
	ExternalVideoID   string        `json:"external_video_id"`
	RunID             int64         `json:"run_id"`
	Category          ErrorCategory `json:"error_category"`
	Message           string        `json:"error_message"`
	Detail            string        `json:"detail,omitempty"`
	Status            ErrorStatus   `json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	ResolvedAt        *time.Time    `json:"resolved_at,omitempty"`
	RetryCount        int           `json:"retry_count"`
	ReasonUnavailable bool          `json:"error_message_unavailable,omitempty"`
}

// unsafeText returns text unchanged when it is safe to expose, and "" plus an
// unavailable flag when non-empty stored text fails vetting.
func publicErrorEntry(e *ErrorEntry) errorEntryView {
	view := errorEntryView{
		RetryRunID:      e.RetryRunID,
		ID:              e.ID,
		VideoID:         e.VideoID,
		ExternalVideoID: e.ExternalVideoID,
		RunID:           e.RunID,
		Category:        e.Category,
		Status:          e.Status,
		CreatedAt:       e.CreatedAt,
		ResolvedAt:      e.ResolvedAt,
		RetryCount:      e.RetryCount,
	}
	var unavailable bool
	view.Message, unavailable = diag.AdmitErrorText(e.Message)
	m := unavailable
	view.Detail, unavailable = diag.AdmitErrorText(e.Detail)
	view.ReasonUnavailable = m || unavailable
	return view
}

type deferredItemView struct {
	*DeferredItem
	Reason                    *DeferredReason `json:"Reason,omitempty"`
	DeferredReasonUnavailable bool            `json:"deferred_reason_unavailable,omitempty"`
}

func publicDeferredItem(item *DeferredItem) deferredItemView {
	view := deferredItemView{DeferredItem: item}
	if item == nil {
		return view
	}
	if item.Reason == "" {
		return view
	}
	if value, ok := diag.AdmitDisposition(string(item.Reason), "queue_reason"); ok {
		reason := DeferredReason(value)
		view.Reason = &reason
	} else {
		view.DeferredReasonUnavailable = true
	}
	return view
}

func publicDeferredItems(items []DeferredItem) []deferredItemView {
	out := make([]deferredItemView, len(items))
	for i := range items {
		out[i] = publicDeferredItem(&items[i])
	}
	return out
}

func publicErrorEntries(entries []ErrorEntry) []errorEntryView {
	out := make([]errorEntryView, len(entries))
	for i := range entries {
		out[i] = publicErrorEntry(&entries[i])
	}
	return out
}

func queueFailureMessage(err error, operation string) string {
	return diag.Encode(err, "unknown", operation)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
