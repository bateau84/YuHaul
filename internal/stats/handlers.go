package stats

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/bateau84/YuHaul/internal/state"
)

// Handler exposes analytics REST endpoints.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler returns an analytics HTTP handler.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

// Handler returns the HTTP handler for analytics routes.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/analytics", h.handleSummary)
	mux.HandleFunc("GET /api/analytics/{$}", h.handleSummary)
	mux.HandleFunc("GET /api/analytics/trends", h.handleTrends)
	mux.HandleFunc("GET /api/analytics/channels", h.handleListChannels)
	mux.HandleFunc("GET /api/analytics/channels/{id}", h.handleGetChannel)
	mux.HandleFunc("GET /api/analytics/channels/{id}/duplicate-provenance", h.handleDuplicateProvenance)
	mux.HandleFunc("GET /api/analytics/channels/{id}/retention-signals", h.handleRetentionSignals)
	mux.HandleFunc("GET /api/analytics/quota", h.handleQuota)

	// Retention playlist management lives under settings but is analytics-owned.
	mux.HandleFunc("GET /api/settings/retention-playlists", h.handleListRetentionPlaylists)
	mux.HandleFunc("POST /api/settings/retention-playlists", h.handleCreateRetentionPlaylist)
	mux.HandleFunc("DELETE /api/settings/retention-playlists/{id}", h.handleDeleteRetentionPlaylist)

	// Run/route stats are served from the run resource for discoverability.
	mux.HandleFunc("GET /api/runs/{id}/stats", h.handleRunStats)
	mux.HandleFunc("GET /api/runs/{id}/route-stats", h.handleRouteStats)
	return mux
}

func (h *Handler) handleRunStats(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	stats, err := h.service.RunStats(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "run stats failed", "error", err, "run_id", id)
		writeError(w, http.StatusInternalServerError, "failed to load run stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) handleRouteStats(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}
	stats, err := h.service.RouteStats(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "route stats failed", "error", err, "run_id", id)
		writeError(w, http.StatusInternalServerError, "failed to load route stats")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"route_stats": stats})
}

func (h *Handler) handleTrends(w http.ResponseWriter, r *http.Request) {
	to := time.Now().UTC()
	from := to.Add(-30 * 24 * time.Hour)
	granularity := "day"

	if v := r.URL.Query().Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			from = t
		}
	}
	if v := r.URL.Query().Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			to = t
		}
	}
	if v := r.URL.Query().Get("granularity"); v != "" {
		granularity = v
	}

	buckets, err := h.service.TemporalStats(r.Context(), from, to, granularity)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "trends failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load trends")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":        from,
		"to":          to,
		"granularity": granularity,
		"buckets":     buckets,
	})
}

func (h *Handler) handleListChannels(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.ChannelStats(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list channel stats failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load channel stats")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": stats})
}

func (h *Handler) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	stats, err := h.service.ChannelDetail(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "channel not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "channel detail failed", "error", err, "channel_id", id)
		writeError(w, http.StatusInternalServerError, "failed to load channel stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (h *Handler) handleDuplicateProvenance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	provenance, err := h.service.DuplicateProvenance(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "duplicate provenance failed", "error", err, "channel_id", id)
		writeError(w, http.StatusInternalServerError, "failed to load duplicate provenance")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provenance": provenance})
}

func (h *Handler) handleRetentionSignals(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	signals, err := h.service.RetentionSignals(r.Context(), id)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "retention signals failed", "error", err, "channel_id", id)
		writeError(w, http.StatusInternalServerError, "failed to load retention signals")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signals": signals})
}

func (h *Handler) handleQuota(w http.ResponseWriter, r *http.Request) {
	budget, breakdown, err := h.service.QuotaStatus(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "quota status failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load quota status")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"budget":    budget,
		"breakdown": breakdown,
	})
}

func (h *Handler) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channels, err := h.service.ChannelStats(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics summary: channel stats failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load analytics summary")
		return
	}

	budget, breakdown, err := h.service.QuotaStatus(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "analytics summary: quota status failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load analytics summary")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channels": channels,
		"quota": map[string]any{
			"budget":    budget,
			"breakdown": breakdown,
		},
	})
}

func (h *Handler) handleListRetentionPlaylists(w http.ResponseWriter, r *http.Request) {
	playlists, err := h.service.RetentionPlaylists(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "list retention playlists failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load retention playlists")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": playlists})
}

func (h *Handler) handleCreateRetentionPlaylist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlaylistID   string `json:"playlist_id"`
		PlaylistName string `json:"playlist_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.PlaylistID == "" {
		writeError(w, http.StatusBadRequest, "playlist_id is required")
		return
	}
	playlist, err := h.service.DesignateRetentionPlaylist(r.Context(), body.PlaylistID, body.PlaylistName)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "create retention playlist failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to designate retention playlist")
		return
	}
	writeJSON(w, http.StatusCreated, playlist)
}

func (h *Handler) handleDeleteRetentionPlaylist(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid playlist id")
		return
	}
	if err := h.service.RemoveRetentionPlaylist(r.Context(), id); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "retention playlist not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "delete retention playlist failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to remove retention playlist")
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
