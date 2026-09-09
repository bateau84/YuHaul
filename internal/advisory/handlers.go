package advisory

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/bateau84/YuHaul/internal/state"
)

// Handler exposes advisory REST endpoints.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns an advisory HTTP handler.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

// Handler returns the HTTP handler for advisory routes.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/analytics/advisories", h.handleListAdvisories)
	mux.HandleFunc("GET /api/analytics/channels/{id}/advisories", h.handleListChannelAdvisories)
	mux.HandleFunc("POST /api/analytics/advisories/{id}/dismiss", h.handleDismiss)
	mux.HandleFunc("POST /api/analytics/advisories/{id}/ignore", h.handleIgnore)
	mux.HandleFunc("POST /api/analytics/advisories/{id}/act", h.handleAct)
	return mux
}

func (h *Handler) handleListAdvisories(w http.ResponseWriter, r *http.Request) {
	advisories, err := h.service.ListActive(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list advisories failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load advisories")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"advisories": advisories})
}

func (h *Handler) handleListChannelAdvisories(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	advisories, err := h.service.ListForChannel(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list channel advisories failed", "error", err, "channel_id", id)
		writeError(w, http.StatusInternalServerError, "failed to load advisories")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"advisories": advisories})
}

func (h *Handler) handleDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid advisory id")
		return
	}
	if err := h.service.Dismiss(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "advisory not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "dismiss advisory failed", "error", err, "advisory_id", id)
		writeError(w, http.StatusInternalServerError, "failed to dismiss advisory")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleIgnore(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid advisory id")
		return
	}
	if err := h.service.Ignore(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "advisory not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "ignore advisory failed", "error", err, "advisory_id", id)
		writeError(w, http.StatusInternalServerError, "failed to ignore advisory")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleAct(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid advisory id")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.service.Act(r.Context(), id, body.Action); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "advisory not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "act on advisory failed", "error", err, "advisory_id", id)
		writeError(w, http.StatusInternalServerError, "failed to record action")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
