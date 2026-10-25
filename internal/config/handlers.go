package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/scheduler"
	"github.com/bateau84/YuHaul/internal/state"
)

// ScheduleProvider reads and writes the user-configured run schedule.
type ScheduleProvider interface {
	GetSchedule(ctx context.Context) (*scheduler.Schedule, error)
	SetSchedule(ctx context.Context, sch *scheduler.Schedule) error
}

// QuotaProvider reads and writes the configured daily API quota limit.
type QuotaProvider interface {
	DailyLimit(ctx context.Context) (int, error)
	SetDailyLimit(ctx context.Context, limit int) error
}

// AuthStatusProvider returns the connected-account status.
type AuthStatusProvider interface {
	Status(ctx context.Context) (*auth.ConnectionStatus, error)
}

// Handler exposes the configuration-version REST endpoints.
type Handler struct {
	service          *Service
	logger           *slog.Logger
	scheduleProvider ScheduleProvider
	quotaProvider    QuotaProvider
	authProvider     AuthStatusProvider
}

// NewHandler returns a config-version HTTP handler.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

// WithScheduleProvider wires the schedule source used by GET /api/settings.
func (h *Handler) WithScheduleProvider(p ScheduleProvider) *Handler {
	h.scheduleProvider = p
	return h
}

// WithQuotaProvider wires the quota source used by GET /api/settings.
func (h *Handler) WithQuotaProvider(p QuotaProvider) *Handler {
	h.quotaProvider = p
	return h
}

// WithAuthStatusProvider wires the account status source used by GET /api/settings.
func (h *Handler) WithAuthStatusProvider(p AuthStatusProvider) *Handler {
	h.authProvider = p
	return h
}

// Handler returns the HTTP handler for config-version routes.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config/versions", h.handleListVersions)
	mux.HandleFunc("GET /api/config/versions/{id}", h.handleGetVersion)
	mux.HandleFunc("GET /api/config/versions/{id}/diff", h.handleDiffVersion)
	mux.HandleFunc("POST /api/config/versions/{id}/restore", h.handleRestoreVersion)
	mux.HandleFunc("GET /api/settings", h.handleSettings)
	mux.HandleFunc("GET /api/settings/{$}", h.handleSettings)
	mux.HandleFunc("PUT /api/settings", h.handleUpdateSettings)
	mux.HandleFunc("PUT /api/settings/{$}", h.handleUpdateSettings)
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
	ids, err := h.service.RouteIdentities(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load route identities failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list config versions")
		return
	}
	public := make([]PublicConfigVersion, 0, len(versions))
	for _, v := range versions {
		pv, err := publicConfigVersion(v, ids)
		if err != nil {
			h.logger.ErrorContext(r.Context(), "project config version failed", "error", err, "id", v.ID)
			writeError(w, http.StatusInternalServerError, "failed to list config versions")
			return
		}
		public = append(public, pv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": public})
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

	ids, err := h.service.RouteIdentities(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load route identities failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load config version")
		return
	}
	public, err := publicConfigVersion(*cv, ids)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "project config version failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load config version")
		return
	}

	writeJSON(w, http.StatusOK, public)
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
	ids, err := h.service.RouteIdentities(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load route identities failed", "error", err, "from", compareTo, "to", id)
		writeError(w, http.StatusInternalServerError, "failed to diff config versions")
		return
	}
	public, err := publicDiffResult(*result, ids)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "project config diff failed", "error", err, "from", compareTo, "to", id)
		writeError(w, http.StatusInternalServerError, "failed to diff config versions")
		return
	}
	writeJSON(w, http.StatusOK, public)
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

	// Validate the historical snapshot's public projection before persisting
	// the restored version, so an identity-registry integrity failure never
	// commits a config version that the history endpoints cannot serve.
	source, err := h.service.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "config version not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "restore config version failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to restore config version")
		return
	}
	ids, err := h.service.RouteIdentities(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load route identities failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to restore config version")
		return
	}
	if _, err := publicSnapshot(source.Snapshot, ids); err != nil {
		h.logger.ErrorContext(r.Context(), "project restored config version failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to restore config version")
		return
	}

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

	public, err := publicConfigVersion(*cv, ids)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "project restored config version failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to restore config version")
		return
	}

	writeJSON(w, http.StatusCreated, public)
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

func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	resp, err := h.settingsResponse(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "load settings failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body struct {
		Schedule           *string  `json:"schedule"`
		QuotaLimit         *int     `json:"quota_limit"`
		DuplicateThreshold *float64 `json:"duplicate_threshold"`
		DuplicateBehavior  *string  `json:"duplicate_behavior"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.Schedule != nil && h.scheduleProvider != nil {
		expr := *body.Schedule
		if expr != "" {
			if _, err := scheduler.ParseSchedule(expr); err != nil {
				writeError(w, http.StatusBadRequest, "invalid schedule expression")
				return
			}
		}
		if err := h.scheduleProvider.SetSchedule(ctx, &scheduler.Schedule{Expression: expr, Enabled: expr != ""}); err != nil {
			h.logger.ErrorContext(ctx, "set schedule failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to update schedule")
			return
		}
	}

	if body.QuotaLimit != nil && h.quotaProvider != nil {
		if *body.QuotaLimit < 0 {
			writeError(w, http.StatusBadRequest, "quota_limit must be non-negative")
			return
		}
		if err := h.quotaProvider.SetDailyLimit(ctx, *body.QuotaLimit); err != nil {
			h.logger.ErrorContext(ctx, "set quota limit failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to update quota limit")
			return
		}
	}

	if body.DuplicateThreshold != nil {
		if err := h.service.SetDuplicateThreshold(ctx, *body.DuplicateThreshold); err != nil {
			h.logger.ErrorContext(ctx, "set duplicate threshold failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to update duplicate threshold")
			return
		}
	}

	// DuplicateBehavior is decoded for legacy string/null admission only.
	// It cannot change fixed continuation or independently create history.

	resp, err := h.settingsResponse(ctx)
	if err != nil {
		h.logger.ErrorContext(ctx, "load settings after update failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) settingsResponse(ctx context.Context) (map[string]any, error) {
	threshold, err := h.service.DuplicateThreshold(ctx)
	if err != nil {
		return nil, err
	}

	behavior, err := h.service.DuplicateBehavior(ctx)
	if err != nil {
		return nil, err
	}

	schedule := ""
	if h.scheduleProvider != nil {
		sch, err := h.scheduleProvider.GetSchedule(ctx)
		if err != nil {
			return nil, err
		}
		if sch != nil {
			schedule = sch.Expression
		}
	}

	quotaLimit := 0
	if h.quotaProvider != nil {
		limit, err := h.quotaProvider.DailyLimit(ctx)
		if err != nil {
			return nil, err
		}
		quotaLimit = limit
	}

	accountConnected := false
	if h.authProvider != nil {
		status, err := h.authProvider.Status(ctx)
		if err != nil {
			return nil, err
		}
		if status != nil {
			accountConnected = status.Connected
		}
	}

	return map[string]any{
		"schedule":            schedule,
		"quota_limit":         quotaLimit,
		"duplicate_threshold": threshold,
		"duplicate_behavior":  behavior,
		"account_connected":   accountConnected,
	}, nil
}

func parseID(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// publicConfigVersion projects a stored ConfigVersion onto its public API
// representation (ADR-011/ARS-004): route identities become the stable
// public UUIDs resolved through the route_identity registry, and action
// types use the public vocabulary (ADR-007). The stored snapshot keeps the
// internal integer route keys and legacy action values and is never mutated.
// A route key without a registry mapping fails the projection — it must
// never be emitted as an integer, omitted, or substituted by route name.
func publicConfigVersion(cv ConfigVersion, ids map[int64]string) (PublicConfigVersion, error) {
	snapshot, err := publicSnapshot(cv.Snapshot, ids)
	if err != nil {
		return PublicConfigVersion{}, err
	}
	return PublicConfigVersion{
		ID:                cv.ID,
		CreatedAt:         cv.CreatedAt,
		Snapshot:          snapshot,
		ChangeDescription: cv.ChangeDescription,
	}, nil
}

// publicDiffResult projects a computed diff onto its public API
// representation. The comparison ran on the stored internal snapshots
// before this projection, so duplicate route names and legacy action values
// never affect which routes count as added, removed, or changed; only the
// emitted identities and action vocabulary change.
func publicDiffResult(d DiffResult, ids map[int64]string) (PublicDiffResult, error) {
	result := PublicDiffResult{
		FromID:          d.FromID,
		ToID:            d.ToID,
		SettingsChanged: d.SettingsChanged,
		RoutesAdded:     make([]PublicSnapshotRoute, 0, len(d.RoutesAdded)),
		RoutesRemoved:   make([]PublicSnapshotRoute, 0, len(d.RoutesRemoved)),
		RoutesChanged:   make([]PublicRouteChange, 0, len(d.RoutesChanged)),
	}
	for _, r := range d.RoutesAdded {
		publicRoute, err := publicSnapshotRoute(r, ids)
		if err != nil {
			return PublicDiffResult{}, err
		}
		result.RoutesAdded = append(result.RoutesAdded, publicRoute)
	}
	for _, r := range d.RoutesRemoved {
		publicRoute, err := publicSnapshotRoute(r, ids)
		if err != nil {
			return PublicDiffResult{}, err
		}
		result.RoutesRemoved = append(result.RoutesRemoved, publicRoute)
	}
	for _, c := range d.RoutesChanged {
		old, err := publicSnapshotRoute(c.Old, ids)
		if err != nil {
			return PublicDiffResult{}, err
		}
		new, err := publicSnapshotRoute(c.New, ids)
		if err != nil {
			return PublicDiffResult{}, err
		}
		result.RoutesChanged = append(result.RoutesChanged, PublicRouteChange{
			ID:      new.ID,
			Old:     old,
			New:     new,
			Changes: c.Changes,
		})
	}
	return result, nil
}

func publicSnapshot(snap Snapshot, ids map[int64]string) (PublicSnapshot, error) {
	routes := make([]PublicSnapshotRoute, 0, len(snap.Routes))
	for _, r := range snap.Routes {
		publicRoute, err := publicSnapshotRoute(r, ids)
		if err != nil {
			return PublicSnapshot{}, err
		}
		routes = append(routes, publicRoute)
	}
	return PublicSnapshot{
		Routes:             routes,
		DuplicateThreshold: snap.DuplicateThreshold,
		DuplicateBehavior:  snap.DuplicateBehavior,
	}, nil
}

func publicSnapshotRoute(r SnapshotRoute, ids map[int64]string) (PublicSnapshotRoute, error) {
	publicID, ok := ids[r.ID]
	if !ok {
		return PublicSnapshotRoute{}, fmt.Errorf("config: route %d has no route identity mapping", r.ID)
	}
	actions := make([]SnapshotAction, 0, len(r.Actions))
	for _, a := range r.Actions {
		a.ActionType = router.ToPublicActionType(a.ActionType)
		actions = append(actions, a)
	}
	return PublicSnapshotRoute{
		ID:                   publicID,
		Name:                 r.Name,
		Description:          r.Description,
		EvaluationOrder:      r.EvaluationOrder,
		IsFinal:              r.IsFinal,
		IsEnabled:            r.IsEnabled,
		ConditionComposition: r.ConditionComposition,
		Conditions:           r.Conditions,
		Actions:              actions,
		Exclusions:           r.Exclusions,
	}, nil
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
