package queue

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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
		slog.ErrorContext(r.Context(), "list errors failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list error queue")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"errors": entries})
}

func (s *Service) handleRetryError(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	entry, err := s.RetryError(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "error entry not found")
			return
		}
		if errors.Is(err, ErrInvalidState) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "retry error failed", "error_entry_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry error entry")
		return
	}

	writeJSON(w, http.StatusOK, entry)
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
		slog.ErrorContext(r.Context(), "dismiss error failed", "error_entry_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to dismiss error entry")
		return
	}

	writeJSON(w, http.StatusOK, entry)
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
		slog.ErrorContext(r.Context(), "list deferred failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list deferred queue")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deferred": items})
}

func (s *Service) handleRetryDeferred(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	item, err := s.RetryDeferred(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deferred item not found")
			return
		}
		if errors.Is(err, ErrInvalidState) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		slog.ErrorContext(r.Context(), "retry deferred failed", "deferred_item_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to retry deferred item")
		return
	}

	writeJSON(w, http.StatusOK, item)
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
		slog.ErrorContext(r.Context(), "discard deferred failed", "deferred_item_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to discard deferred item")
		return
	}

	writeJSON(w, http.StatusOK, item)
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
