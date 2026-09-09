package config

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/bateau84/YuHaul/internal/state"
)

// Handler exposes the configuration-version REST endpoints.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns a config-version HTTP handler.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

// Handler returns the HTTP handler for config-version routes.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config/versions", h.handleListVersions)
	mux.HandleFunc("GET /api/config/versions/{id}", h.handleGetVersion)
	mux.HandleFunc("GET /api/config/versions/{id}/diff", h.handleDiffVersion)
	mux.HandleFunc("POST /api/config/versions/{id}/restore", h.handleRestoreVersion)
	mux.HandleFunc("GET /api/settings/duplicate-threshold", h.handleGetThreshold)
	mux.HandleFunc("PUT /api/settings/duplicate-threshold", h.handleSetThreshold)
	return mux
}

func (h *Handler) handleListVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := h.service.List(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list config versions failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list config versions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (h *Handler) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config version id")
		return
	}

	cv, err := h.service.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "config version not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "get config version failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load config version")
		return
	}

	writeJSON(w, http.StatusOK, cv)
}

func (h *Handler) handleDiffVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config version id")
		return
	}

	compareTo := id
	if v := r.URL.Query().Get("compare_to"); v != "" {
		compareTo, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid compare_to id")
			return
		}
	} else {
		// Default to the predecessor.
		cv, err := h.service.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				writeError(w, http.StatusNotFound, "config version not found")
				return
			}
			h.logger.ErrorContext(r.Context(), "diff config version failed", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to diff config version")
			return
		}
		if cv.ID > 1 {
			compareTo = cv.ID - 1
		}
	}

	result, err := h.service.Diff(r.Context(), compareTo, id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "diff config versions failed", "error", err, "from", compareTo, "to", id)
		writeError(w, http.StatusInternalServerError, "failed to diff config versions")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid config version id")
		return
	}

	var body struct {
		Description string `json:"description"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	cv, err := h.service.Restore(r.Context(), id, body.Description)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "config version not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "restore config version failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to restore config version")
		return
	}

	writeJSON(w, http.StatusCreated, cv)
}

func (h *Handler) handleGetThreshold(w http.ResponseWriter, r *http.Request) {
	threshold, err := h.service.DuplicateThreshold(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get duplicate threshold failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load threshold")
		return
	}
	behavior, err := h.service.DuplicateBehavior(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "get duplicate behavior failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load behavior")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"threshold":          threshold,
		"duplicate_behavior": behavior,
		"default_threshold":  0.5,
		"range":              map[string]float64{"min": 0.0, "max": 1.0},
	})
}

func (h *Handler) handleSetThreshold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Threshold float64 `json:"threshold"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.service.SetDuplicateThreshold(r.Context(), body.Threshold); err != nil {
		h.logger.ErrorContext(r.Context(), "set duplicate threshold failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to set threshold")
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
