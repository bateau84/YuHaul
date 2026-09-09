// Package server exposes the YuHaul HTTP API, including manual run triggers and
// SSE run-progress streams.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// Pipeline is the subset of the pipeline package used by the HTTP server.
type Pipeline interface {
	StartRun(ctx context.Context, runType state.RunType) (*state.Run, error)
	Execute(ctx context.Context, runID int64)
	DryRun(ctx context.Context, draft *config.Snapshot) (*state.Run, error)
	ExecuteDryRun(ctx context.Context, runID int64)
	Replay(ctx context.Context, configVersionID *int64) (*state.Run, error)
	ExecuteReplay(ctx context.Context, runID int64)
	Backtest(ctx context.Context, proposed config.Snapshot) (*state.Run, error)
	ExecuteBacktest(ctx context.Context, runID int64)
}

// ConfigHandler is the subset of config.Handler used by the server.
type ConfigHandler interface {
	Handler() http.Handler
}

// AnalyticsHandler serves statistics and advisory endpoints.
type AnalyticsHandler interface {
	Handler() http.Handler
}

// QueueHandler serves error and deferred queue endpoints.
type QueueHandler interface {
	ErrorHandler() http.Handler
	DeferredHandler() http.Handler
}

// RouteStore is the subset of router.Store used by the HTTP server.
type RouteStore interface {
	CreateRoute(ctx context.Context, r *router.Route) (int64, error)
	GetRoute(ctx context.Context, id int64) (*router.Route, error)
	ListRoutes(ctx context.Context) ([]router.Route, error)
	UpdateRoute(ctx context.Context, r *router.Route) error
	DeleteRoute(ctx context.Context, id int64) error
	ReorderRoutes(ctx context.Context, routeIDs []int64) error
}

// RunStore is the subset of state.Store used by the HTTP server for run queries.
type RunStore interface {
	ListRuns(ctx context.Context) ([]state.Run, error)
	GetRun(ctx context.Context, id int64) (*state.Run, error)
}

// Overview is the dashboard summary returned by GET /api/overview.
type Overview struct {
	LastRun       *state.Run `json:"last_run"`
	RunsToday     int        `json:"runs_today"`
	Quota         any        `json:"quota"`
	ErrorCount    int        `json:"error_count"`
	DeferredCount int        `json:"deferred_count"`
}

// OverviewProvider returns the dashboard summary data.
type OverviewProvider interface {
	Overview(ctx context.Context) (*Overview, error)
}

// Server serves the YuHaul REST API and SSE endpoints.
type Server struct {
	pipeline         Pipeline
	broadcast        *pipeline.Broadcaster
	configHandler    ConfigHandler
	analyticsHandler AnalyticsHandler
	advisoryHandler  AnalyticsHandler
	queueHandler     QueueHandler
	routeStore       RouteStore
	runStore         RunStore
	overviewProvider OverviewProvider
	logger           *slog.Logger
}

// Option configures a Server.
type Option func(*Server)

// WithServerLogger sets the structured logger.
func WithServerLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithConfigHandler registers the configuration-version HTTP handler.
func WithConfigHandler(h ConfigHandler) Option {
	return func(s *Server) {
		if h != nil {
			s.configHandler = h
		}
	}
}

// WithAnalyticsHandler registers the statistics HTTP handler.
func WithAnalyticsHandler(h AnalyticsHandler) Option {
	return func(s *Server) {
		if h != nil {
			s.analyticsHandler = h
		}
	}
}

// WithAdvisoryHandler registers the advisory HTTP handler.
func WithAdvisoryHandler(h AnalyticsHandler) Option {
	return func(s *Server) {
		if h != nil {
			s.advisoryHandler = h
		}
	}
}

// WithQueueHandler registers the error and deferred queue HTTP handlers.
func WithQueueHandler(h QueueHandler) Option {
	return func(s *Server) {
		if h != nil {
			s.queueHandler = h
		}
	}
}

// WithRouteStore registers the route configuration store.
func WithRouteStore(store RouteStore) Option {
	return func(s *Server) {
		if store != nil {
			s.routeStore = store
		}
	}
}

// WithRunStore registers the run query store.
func WithRunStore(store RunStore) Option {
	return func(s *Server) {
		if store != nil {
			s.runStore = store
		}
	}
}

// WithOverviewProvider registers the dashboard overview provider.
func WithOverviewProvider(provider OverviewProvider) Option {
	return func(s *Server) {
		if provider != nil {
			s.overviewProvider = provider
		}
	}
}

// NewServer returns an API server.
func NewServer(pipeline Pipeline, broadcast *pipeline.Broadcaster, opts ...Option) *Server {
	s := &Server{
		pipeline:         pipeline,
		broadcast:        broadcast,
		configHandler:    &noopHandler{},
		analyticsHandler: &noopHandler{},
		advisoryHandler:  &noopHandler{},
		queueHandler:     noopQueueHandler{},
		routeStore:       noopRouteStore{},
		runStore:         noopRunStore{},
		overviewProvider: noopOverviewProvider{},
		logger:           slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type noopHandler struct{}

func (n *noopHandler) Handler() http.Handler {
	return http.NewServeMux()
}

type noopRouteStore struct{}

func (noopRouteStore) CreateRoute(context.Context, *router.Route) (int64, error) {
	return 0, errors.New("route store not configured")
}
func (noopRouteStore) GetRoute(context.Context, int64) (*router.Route, error) {
	return nil, errors.New("route store not configured")
}
func (noopRouteStore) ListRoutes(context.Context) ([]router.Route, error) {
	return nil, errors.New("route store not configured")
}
func (noopRouteStore) UpdateRoute(context.Context, *router.Route) error {
	return errors.New("route store not configured")
}
func (noopRouteStore) DeleteRoute(context.Context, int64) error {
	return errors.New("route store not configured")
}
func (noopRouteStore) ReorderRoutes(context.Context, []int64) error {
	return errors.New("route store not configured")
}

type noopRunStore struct{}

func (noopRunStore) ListRuns(context.Context) ([]state.Run, error) {
	return nil, errors.New("run store not configured")
}
func (noopRunStore) GetRun(context.Context, int64) (*state.Run, error) {
	return nil, errors.New("run store not configured")
}

type noopOverviewProvider struct{}

func (noopOverviewProvider) Overview(context.Context) (*Overview, error) {
	return nil, errors.New("overview provider not configured")
}

type noopQueueHandler struct{}

func (noopQueueHandler) ErrorHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "queue handler not configured")
	})
	return mux
}
func (noopQueueHandler) DeferredHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/{$}", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "queue handler not configured")
	})
	return mux
}

// Handler returns the HTTP handler for the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("POST /api/runs/dry-run", s.handleDryRun)
	mux.HandleFunc("POST /api/runs/replay", s.handleReplay)
	mux.HandleFunc("POST /api/backtest", s.handleBacktest)
	mux.HandleFunc("GET /api/runs", s.handleListRuns)
	mux.HandleFunc("GET /api/runs/{id}", s.handleGetRun)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.handleRunStream)
	mux.HandleFunc("GET /api/overview", s.handleOverview)

	// Route CRUD endpoints.
	mux.HandleFunc("GET /api/routes", s.handleListRoutes)
	mux.HandleFunc("POST /api/routes", s.handleCreateRoute)
	mux.HandleFunc("GET /api/routes/{id}", s.handleGetRoute)
	mux.HandleFunc("PUT /api/routes/{id}", s.handleUpdateRoute)
	mux.HandleFunc("DELETE /api/routes/{id}", s.handleDeleteRoute)
	mux.HandleFunc("PUT /api/routes/reorder", s.handleReorderRoutes)

	// Configuration-version endpoints are served by a dedicated handler.
	mux.Handle("/api/config/", s.configHandler.Handler())
	mux.Handle("/api/settings/", s.configHandler.Handler())

	// Queue handlers are served from dedicated sub-paths.
	mux.Handle("/api/errors/", http.StripPrefix("/api/errors", s.queueHandler.ErrorHandler()))
	mux.Handle("/api/deferred/", http.StripPrefix("/api/deferred", s.queueHandler.DeferredHandler()))

	// Advisory routes are more specific than the analytics prefix; register
	// them first so they take precedence over the broader analytics handler.
	mux.Handle("/api/analytics/advisories", s.advisoryHandler.Handler())
	mux.Handle("/api/analytics/channels/{id}/advisories", s.advisoryHandler.Handler())
	mux.Handle("/api/analytics/", s.analyticsHandler.Handler())
	return mux
}

func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.pipeline.StartRun(r.Context(), state.RunTypeManual)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "failed to start run", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start run")
		return
	}

	// Execute the pipeline asynchronously so the HTTP response returns
	// immediately with the run ID.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.pipeline.Execute(ctx, run.ID)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id": run.ID,
		"status": string(run.Status),
	})
}

func (s *Server) handleDryRun(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DraftConfiguration *config.Snapshot `json:"draft_configuration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		s.logger.ErrorContext(r.Context(), "failed to decode dry-run body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.DraftConfiguration != nil {
		if body.DraftConfiguration.DuplicateThreshold < 0.0 || body.DraftConfiguration.DuplicateThreshold > 1.0 {
			writeError(w, http.StatusBadRequest, "duplicate_threshold must be between 0.0 and 1.0")
			return
		}
	}

	run, err := s.pipeline.DryRun(r.Context(), body.DraftConfiguration)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "failed to start dry-run", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start dry-run")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.pipeline.ExecuteDryRun(ctx, run.ID)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id":   run.ID,
		"run_type": string(run.RunType),
		"status":   string(run.Status),
	})
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	var cfgID *int64

	if q := r.URL.Query().Get("config_version_id"); q != "" {
		id, err := strconv.ParseInt(q, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid config_version_id")
			return
		}
		cfgID = &id
	} else {
		var body struct {
			ConfigVersionID *int64 `json:"config_version_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
			s.logger.ErrorContext(r.Context(), "failed to decode replay body", "error", err)
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		cfgID = body.ConfigVersionID
	}

	run, err := s.pipeline.Replay(r.Context(), cfgID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "failed to start replay", "error", err)
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "config version not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to start replay")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.pipeline.ExecuteReplay(ctx, run.ID)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id":   run.ID,
		"run_type": string(run.RunType),
		"status":   string(run.Status),
	})
}

func (s *Server) handleBacktest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProposedConfiguration config.Snapshot `json:"proposed_configuration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.logger.ErrorContext(r.Context(), "failed to decode backtest body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if body.ProposedConfiguration.DuplicateThreshold < 0.0 || body.ProposedConfiguration.DuplicateThreshold > 1.0 {
		writeError(w, http.StatusBadRequest, "duplicate_threshold must be between 0.0 and 1.0")
		return
	}

	run, err := s.pipeline.Backtest(r.Context(), body.ProposedConfiguration)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "failed to start backtest", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start backtest")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		s.pipeline.ExecuteBacktest(ctx, run.ID)
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"run_id":   run.ID,
		"run_type": string(run.RunType),
		"status":   string(run.Status),
	})
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.runStore.ListRuns(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list runs failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	run, err := s.runStore.GetRun(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get run failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.overviewProvider.Overview(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "overview failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (s *Server) handleRunStream(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := s.broadcast.Subscribe(runID)
	defer s.broadcast.Unsubscribe(runID, ch)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSE(w, ev); err != nil {
				s.logger.DebugContext(r.Context(), "sse write failed", "error", err)
				return
			}
			flusher.Flush()
			if ev.Type == pipeline.EventRunCompleted || ev.Type == pipeline.EventRunError {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ":ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) handleListRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := s.routeStore.ListRoutes(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list routes failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list routes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"routes": routes})
}

func (s *Server) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	route, err := s.routeStore.GetRoute(r.Context(), id)
	if err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get route failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load route")
		return
	}
	writeJSON(w, http.StatusOK, route)
}

func (s *Server) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRouteRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	route := req.toRoute()
	id, err := s.routeStore.CreateRoute(r.Context(), route)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "create route failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create route")
		return
	}

	created, err := s.routeStore.GetRoute(r.Context(), id)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load created route failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load created route")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	req, err := decodeRouteRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	route := req.toRoute()
	route.ID = id
	if err := s.routeStore.UpdateRoute(r.Context(), route); err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "update route failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to update route")
		return
	}

	updated, err := s.routeStore.GetRoute(r.Context(), id)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load updated route failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to load updated route")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	if err := s.routeStore.DeleteRoute(r.Context(), id); err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "delete route failed", "error", err, "id", id)
		writeError(w, http.StatusInternalServerError, "failed to delete route")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderRoutes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RouteIDs []int64 `json:"route_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.RouteIDs) == 0 {
		writeError(w, http.StatusBadRequest, "route_ids is required")
		return
	}

	if err := s.routeStore.ReorderRoutes(r.Context(), body.RouteIDs); err != nil {
		s.logger.ErrorContext(r.Context(), "reorder routes failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reorder routes")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type routeRequest struct {
	Name                 string                  `json:"name"`
	Description          string                  `json:"description"`
	EvaluationOrder      int                     `json:"evaluation_order"`
	IsFinal              bool                    `json:"is_final"`
	IsEnabled            bool                    `json:"is_enabled"`
	ConditionComposition string                  `json:"condition_composition"`
	Conditions           []router.RouteCondition `json:"conditions"`
	Actions              []router.RouteAction    `json:"actions"`
	Exclusions           []router.RouteExclusion `json:"exclusions"`
}

func decodeRouteRequest(r *http.Request) (*routeRequest, error) {
	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid request body")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("name is required")
	}
	comp := strings.ToUpper(req.ConditionComposition)
	if comp != "AND" && comp != "OR" {
		return nil, fmt.Errorf("condition_composition must be AND or OR")
	}
	req.ConditionComposition = comp
	return &req, nil
}

func (req *routeRequest) toRoute() *router.Route {
	return &router.Route{
		Name:                 req.Name,
		Description:          req.Description,
		EvaluationOrder:      req.EvaluationOrder,
		IsFinal:              req.IsFinal,
		IsEnabled:            req.IsEnabled,
		ConditionComposition: req.ConditionComposition,
		Conditions:           req.Conditions,
		Actions:              req.Actions,
		Exclusions:           req.Exclusions,
	}
}

func writeSSE(w http.ResponseWriter, ev pipeline.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, string(data))
	return err
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
