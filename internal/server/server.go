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
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// Pipeline is the subset of the pipeline package used by the HTTP server.
// Dry-run and backtest proposals cross this seam as public configurations
// whose route IDs are canonical UUIDv4 strings; the pipeline translates them
// to internal integer keys in its single proposal-ingest transaction
// (ADR-012).
type Pipeline interface {
	StartRun(ctx context.Context, runType state.RunType) (*state.Run, error)
	Execute(ctx context.Context, runID int64)
	DryRun(ctx context.Context, draft *config.ProposalSnapshot) (*state.Run, error)
	ExecuteDryRun(ctx context.Context, runID int64)
	Replay(ctx context.Context, configVersionID *int64) (*state.Run, error)
	ExecuteReplay(ctx context.Context, runID int64)
	Backtest(ctx context.Context, proposed *config.ProposalSnapshot) (*state.Run, error)
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

// RouteStore is the subset of router.Store used by the HTTP server. Route
// identity crosses this seam as the stable public UUID (ARS-004); the
// internal integer key is resolved through the route_identity registry and
// never leaves the API boundary.
type RouteStore interface {
	CreateRoute(ctx context.Context, r *router.Route) (int64, error)
	GetRouteByPublicID(ctx context.Context, publicID string) (*router.Route, error)
	ResolveRouteID(ctx context.Context, publicID string) (int64, error)
	ListRoutes(ctx context.Context) ([]router.Route, error)
	UpdateRoute(ctx context.Context, r *router.Route) error
	DeleteRoute(ctx context.Context, id int64) error
	ReorderRoutes(ctx context.Context, routeIDs []int64) error
	ListRouteNamesIncludingDeleted(ctx context.Context) (map[int64]string, error)
	ListRouteIdentities(ctx context.Context) (map[int64]string, error)
	ListRouteMatchCounts(ctx context.Context) (map[int64]int64, error)
}

// RunStore is the subset of state.Store used by the HTTP server for run queries.
type RunStore interface {
	ListRuns(ctx context.Context) ([]state.Run, error)
	GetRun(ctx context.Context, id int64) (*state.Run, error)
}

// RunVideoStore is the subset of state.Store used by the run video handlers.
// It is defined in run_handlers.go and repeated here for the Server field.
type RunVideoStore interface {
	ListRunVideos(ctx context.Context, runID int64, limit, offset int, outcomeFilter state.VideoFinalOutcome) ([]state.Video, int64, error)
	GetRun(ctx context.Context, id int64) (*state.Run, error)
	GetVideoByExternalIDInRun(ctx context.Context, runID int64, videoID string) (*state.Video, error)
	GetRouteDecisionsForVideoInRun(ctx context.Context, videoID, runID int64) ([]state.RouteDecision, error)
	GetConditionAuditsForDecision(ctx context.Context, decisionID int64) ([]state.ConditionAudit, error)
	GetExecutedActionsForVideoInRun(ctx context.Context, videoID, runID int64) ([]state.ExecutedAction, error)
}

// ConfigVersionStore is the subset of config.Store used by the run handlers.
// Run-video evidence projects route labels from the run's immutable
// ConfigVersion snapshot (OC-003/ARS-004), so the handler needs the stored
// snapshot for the run's linked config version.
type ConfigVersionStore interface {
	Get(ctx context.Context, id int64) (*config.ConfigVersion, error)
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

// PlaylistProvider returns the authenticated user's YouTube playlists.
type PlaylistProvider interface {
	ListPlaylists(ctx context.Context) ([]Playlist, error)
}

// CorpusImportHandler serves the ARS-002 corpus import endpoints under
// /api/corpus/. The manager behind it is process-local; the server only mounts
// the routes.
type CorpusImportHandler interface {
	Handler() http.Handler
}

// Playlist is the shape returned by GET /api/playlists.
type Playlist struct {
	PlaylistID   string `json:"playlist_id"`
	PlaylistName string `json:"playlist_name"`
}

// Server serves the YuHaul REST API and SSE endpoints.
type Server struct {
	pipeline            Pipeline
	broadcast           *pipeline.Broadcaster
	configHandler       ConfigHandler
	analyticsHandler    AnalyticsHandler
	advisoryHandler     AnalyticsHandler
	queueHandler        QueueHandler
	routeStore          RouteStore
	runStore            RunStore
	runVideoStore       RunVideoStore
	configVersionStore  ConfigVersionStore
	overviewProvider    OverviewProvider
	playlistProvider    PlaylistProvider
	corpusImportHandler CorpusImportHandler
	logger              *slog.Logger
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

// WithRunVideoStore registers the store used by run video and decision detail
// endpoints.
func WithRunVideoStore(store RunVideoStore) Option {
	return func(s *Server) {
		if store != nil {
			s.runVideoStore = store
		}
	}
}

// WithConfigVersionStore registers the store used by run handlers to read the
// immutable ConfigVersion snapshot of a run.
func WithConfigVersionStore(store ConfigVersionStore) Option {
	return func(s *Server) {
		if store != nil {
			s.configVersionStore = store
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

// WithPlaylistProvider registers the provider used by GET /api/playlists.
func WithPlaylistProvider(provider PlaylistProvider) Option {
	return func(s *Server) {
		if provider != nil {
			s.playlistProvider = provider
		}
	}
}

// WithCorpusImportHandler registers the corpus import HTTP handler
// (ARS-002 /api/corpus/* endpoints).
func WithCorpusImportHandler(h CorpusImportHandler) Option {
	return func(s *Server) {
		if h != nil {
			s.corpusImportHandler = h
		}
	}
}

// NewServer returns an API server.
func NewServer(pipeline Pipeline, broadcast *pipeline.Broadcaster, opts ...Option) *Server {
	s := &Server{
		pipeline:            pipeline,
		broadcast:           broadcast,
		configHandler:       &noopHandler{},
		analyticsHandler:    &noopHandler{},
		advisoryHandler:     &noopHandler{},
		queueHandler:        noopQueueHandler{},
		routeStore:          noopRouteStore{},
		runStore:            noopRunStore{},
		runVideoStore:       noopRunVideoStore{},
		configVersionStore:  noopConfigVersionStore{},
		overviewProvider:    noopOverviewProvider{},
		playlistProvider:    noopPlaylistProvider{},
		corpusImportHandler: noopCorpusImportHandler{},
		logger:              slog.Default(),
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
func (noopRouteStore) GetRouteByPublicID(context.Context, string) (*router.Route, error) {
	return nil, errors.New("route store not configured")
}
func (noopRouteStore) ResolveRouteID(context.Context, string) (int64, error) {
	return 0, errors.New("route store not configured")
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
func (noopRouteStore) ListRouteNamesIncludingDeleted(context.Context) (map[int64]string, error) {
	return nil, errors.New("route store not configured")
}

func (noopRouteStore) ListRouteIdentities(context.Context) (map[int64]string, error) {
	return nil, errors.New("route store not configured")
}

func (noopRouteStore) ListRouteMatchCounts(context.Context) (map[int64]int64, error) {
	return nil, errors.New("route store not configured")
}

type noopRunStore struct{}

func (noopRunStore) ListRuns(context.Context) ([]state.Run, error) {
	return nil, errors.New("run store not configured")
}
func (noopRunStore) GetRun(context.Context, int64) (*state.Run, error) {
	return nil, errors.New("run store not configured")
}

type noopRunVideoStore struct{}

func (noopRunVideoStore) ListRunVideos(context.Context, int64, int, int, state.VideoFinalOutcome) ([]state.Video, int64, error) {
	return nil, 0, errors.New("run video store not configured")
}
func (noopRunVideoStore) GetRun(context.Context, int64) (*state.Run, error) {
	return nil, errors.New("run video store not configured")
}
func (noopRunVideoStore) GetVideoByExternalIDInRun(context.Context, int64, string) (*state.Video, error) {
	return nil, errors.New("run video store not configured")
}
func (noopRunVideoStore) GetRouteDecisionsForVideoInRun(context.Context, int64, int64) ([]state.RouteDecision, error) {
	return nil, errors.New("run video store not configured")
}
func (noopRunVideoStore) GetConditionAuditsForDecision(context.Context, int64) ([]state.ConditionAudit, error) {
	return nil, errors.New("run video store not configured")
}
func (noopRunVideoStore) GetExecutedActionsForVideoInRun(context.Context, int64, int64) ([]state.ExecutedAction, error) {
	return nil, errors.New("run video store not configured")
}

// noopConfigVersionStore reports "not found" so run handlers treat the run as
// having no usable configuration snapshot (OC-003 incomplete context) and fall
// back to current persisted route labels instead of failing the endpoint.
type noopConfigVersionStore struct{}

func (noopConfigVersionStore) Get(context.Context, int64) (*config.ConfigVersion, error) {
	return nil, state.ErrNotFound
}

type noopOverviewProvider struct{}

func (noopOverviewProvider) Overview(context.Context) (*Overview, error) {
	return nil, errors.New("overview provider not configured")
}

type noopPlaylistProvider struct{}

func (noopPlaylistProvider) ListPlaylists(context.Context) ([]Playlist, error) {
	return nil, errors.New("playlist provider not configured")
}

// noopCorpusImportHandler reports "not configured" for every corpus import
// route so an unwired server fails loud instead of routing /api/corpus/* into
// the SPA fallback (ARS-002).
type noopCorpusImportHandler struct{}

func (noopCorpusImportHandler) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "corpus import handler not configured")
	})
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
	mux.HandleFunc("GET /api/runs/{id}/videos", s.handleListRunVideos)
	mux.HandleFunc("GET /api/runs/{id}/videos/{videoId}", s.handleGetRunVideo)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.handleRunStream)
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/playlists", s.handleListPlaylists)

	// Route CRUD endpoints.
	mux.HandleFunc("GET /api/routes", s.handleListRoutes)
	mux.HandleFunc("POST /api/routes", s.handleCreateRoute)
	mux.HandleFunc("GET /api/routes/{$}", s.handleListRoutes)
	mux.HandleFunc("POST /api/routes/{$}", s.handleCreateRoute)
	mux.HandleFunc("GET /api/routes/{id}", s.handleGetRoute)
	mux.HandleFunc("PUT /api/routes/{id}", s.handleUpdateRoute)
	mux.HandleFunc("DELETE /api/routes/{id}", s.handleDeleteRoute)
	mux.HandleFunc("PUT /api/routes/reorder", s.handleReorderRoutes)

	// Analytics-owned endpoints that would otherwise be shadowed by the
	// broader /api/settings/ and /api/analytics/ prefix mounts. These must
	// be registered before the prefix handlers so they take precedence.
	mux.Handle("/api/settings/retention-playlists", s.analyticsHandler.Handler())
	mux.Handle("/api/settings/retention-playlists/{id}", s.analyticsHandler.Handler())
	mux.Handle("/api/runs/{id}/stats", s.analyticsHandler.Handler())
	mux.Handle("/api/runs/{id}/route-stats", s.analyticsHandler.Handler())

	// Configuration-version endpoints are served by a dedicated handler.
	mux.Handle("/api/config/", s.configHandler.Handler())
	mux.Handle("/api/settings", s.configHandler.Handler())
	mux.Handle("/api/settings/{$}", s.configHandler.Handler())
	mux.Handle("/api/settings/", s.configHandler.Handler())

	// Queue handlers are served from legacy /api/errors and /api/deferred
	// paths, plus the newer /api/queue/* namespace.
	mux.Handle("/api/errors", stripPrefix("/api/errors", s.queueHandler.ErrorHandler()))
	mux.Handle("/api/errors/", stripPrefix("/api/errors", s.queueHandler.ErrorHandler()))
	mux.Handle("/api/deferred", stripPrefix("/api/deferred", s.queueHandler.DeferredHandler()))
	mux.Handle("/api/deferred/", stripPrefix("/api/deferred", s.queueHandler.DeferredHandler()))
	mux.Handle("/api/queue/errors", stripPrefix("/api/queue/errors", s.queueHandler.ErrorHandler()))
	mux.Handle("/api/queue/errors/", stripPrefix("/api/queue/errors", s.queueHandler.ErrorHandler()))
	mux.Handle("/api/queue/deferred", stripPrefix("/api/queue/deferred", s.queueHandler.DeferredHandler()))
	mux.Handle("/api/queue/deferred/", stripPrefix("/api/queue/deferred", s.queueHandler.DeferredHandler()))

	// Advisory routes are more specific than the analytics prefix; register
	// them first so they take precedence over the broader analytics handler.
	mux.Handle("/api/analytics/advisories", s.advisoryHandler.Handler())
	mux.Handle("/api/analytics/channels/{id}/advisories", s.advisoryHandler.Handler())
	mux.Handle("/api/analytics", s.analyticsHandler.Handler())
	mux.Handle("/api/analytics/{$}", s.analyticsHandler.Handler())
	mux.Handle("/api/analytics/", s.analyticsHandler.Handler())

	// Corpus import endpoints (ARS-002) are served by a dedicated handler
	// that owns its full /api/corpus/* paths.
	mux.Handle("/api/corpus/", s.corpusImportHandler.Handler())
	return mux
}

// stripPrefix is like http.StripPrefix but guarantees a leading slash on the
// forwarded path so that a request for the prefix itself (e.g. /api/errors)
// is routed to the sub-handler's root (/) instead of becoming an empty path
// that triggers a redirect.
func stripPrefix(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, prefix)
		if p == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		if p == "" || p[0] != '/' {
			p = "/" + p
		}
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = new(url.URL)
		*r2.URL = *r.URL
		r2.URL.Path = p
		h.ServeHTTP(w, r2)
	})
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
		DraftConfiguration *proposalRequest `json:"draft_configuration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err.Error() != "EOF" {
		s.logger.ErrorContext(r.Context(), "failed to decode dry-run body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var draft *config.ProposalSnapshot
	if body.DraftConfiguration != nil {
		snap, err := body.DraftConfiguration.snapshot()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if snap.DuplicateThreshold < 0.0 || snap.DuplicateThreshold > 1.0 {
			writeError(w, http.StatusBadRequest, "duplicate_threshold must be between 0.0 and 1.0")
			return
		}
		if err := normalizeProposalActions(snap); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		draft = snap
	}

	run, err := s.pipeline.DryRun(r.Context(), draft)
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
		ProposedConfiguration *proposalRequest `json:"proposed_configuration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.logger.ErrorContext(r.Context(), "failed to decode backtest body", "error", err)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// An absent proposed_configuration keeps its historical behavior: the
	// backtest runs with an empty proposal snapshot.
	req := body.ProposedConfiguration
	if req == nil {
		req = &proposalRequest{}
	}

	proposed, err := req.snapshot()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if proposed.DuplicateThreshold < 0.0 || proposed.DuplicateThreshold > 1.0 {
		writeError(w, http.StatusBadRequest, "duplicate_threshold must be between 0.0 and 1.0")
		return
	}

	if err := normalizeProposalActions(proposed); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	run, err := s.pipeline.Backtest(r.Context(), proposed)
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
	if runs == nil {
		runs = []state.Run{}
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

func (s *Server) handleListPlaylists(w http.ResponseWriter, r *http.Request) {
	playlists, err := s.playlistProvider.ListPlaylists(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list playlists failed", "error", err)
		msg := "failed to list playlists"
		if err.Error() == "playlist provider not configured" || strings.Contains(err.Error(), "not authenticated") {
			msg = "not authenticated"
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"playlists": playlists})
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
	if routes == nil {
		routes = []router.Route{}
	}

	counts, err := s.routeStore.ListRouteMatchCounts(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list route match counts failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list routes")
		return
	}
	for i := range routes {
		routes[i].MatchCount = counts[routes[i].ID]
	}

	entries := make([]routeListEntry, 0, len(routes))
	for i := range routes {
		entry, err := newRouteResponse(&routes[i])
		if err != nil {
			// A route without a resolvable public identity fails the whole
			// response; it is never serialized with an integer or omitted
			// silently (ARS-004 failure behavior).
			s.logger.ErrorContext(r.Context(), "serialize route failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list routes")
			return
		}
		entries = append(entries, routeListEntry{
			routeResponse: entry,
			MatchCount:    routes[i].MatchCount,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"routes": entries})
}

func (s *Server) handleGetRoute(w http.ResponseWriter, r *http.Request) {
	publicID, ok := parseRoutePublicID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	route, err := s.routeStore.GetRouteByPublicID(r.Context(), publicID)
	if err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to load route")
		return
	}
	resp, err := newRouteResponse(route)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "serialize route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to load route")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCreateRoute(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRouteRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// ARS-004: the server assigns the UUID. A caller-supplied route ID —
	// numeric, UUID, or otherwise — is invalid input and never an alias.
	if len(req.ID) > 0 {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	route := req.toRoute()
	if _, err := s.routeStore.CreateRoute(r.Context(), route); err != nil {
		s.logger.ErrorContext(r.Context(), "create route failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create route")
		return
	}

	created, err := s.routeStore.GetRouteByPublicID(r.Context(), route.PublicID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load created route failed", "error", err, "id", route.PublicID)
		writeError(w, http.StatusInternalServerError, "failed to load created route")
		return
	}
	resp, err := newRouteResponse(created)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "serialize route failed", "error", err, "id", route.PublicID)
		writeError(w, http.StatusInternalServerError, "failed to load created route")
		return
	}
	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleUpdateRoute(w http.ResponseWriter, r *http.Request) {
	publicID, ok := parseRoutePublicID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	req, err := decodeRouteRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := s.routeStore.GetRouteByPublicID(r.Context(), publicID)
	if err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "update route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to update route")
		return
	}

	// The path UUID identifies the route; the registry mapping is never
	// rewritten, so the public identity survives rename and reconfiguration.
	route := req.toRoute()
	route.ID = existing.ID
	if err := s.routeStore.UpdateRoute(r.Context(), route); err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "update route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to update route")
		return
	}

	updated, err := s.routeStore.GetRouteByPublicID(r.Context(), publicID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load updated route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to load updated route")
		return
	}
	resp, err := newRouteResponse(updated)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "serialize route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to load updated route")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeleteRoute(w http.ResponseWriter, r *http.Request) {
	publicID, ok := parseRoutePublicID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid route id")
		return
	}

	id, err := s.routeStore.ResolveRouteID(r.Context(), publicID)
	if err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "delete route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to delete route")
		return
	}

	if err := s.routeStore.DeleteRoute(r.Context(), id); err != nil {
		if errors.Is(err, router.ErrRouteNotFound) {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "delete route failed", "error", err, "id", publicID)
		writeError(w, http.StatusInternalServerError, "failed to delete route")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReorderRoutes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RouteIDs []string `json:"route_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(body.RouteIDs) == 0 {
		writeError(w, http.StatusBadRequest, "route_ids is required")
		return
	}

	publicIDs := make([]string, len(body.RouteIDs))
	seen := make(map[string]bool, len(body.RouteIDs))
	for i, raw := range body.RouteIDs {
		publicID, ok := parseRoutePublicID(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid route id")
			return
		}
		if seen[publicID] {
			writeError(w, http.StatusBadRequest, "duplicate route id in route_ids")
			return
		}
		seen[publicID] = true
		publicIDs[i] = publicID
	}

	// The reorder request is a permutation of the active route set: every
	// entry must name an active route, and every active route must appear.
	active, err := s.routeStore.ListRoutes(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "reorder routes failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reorder routes")
		return
	}
	activeIDs := make(map[string]bool, len(active))
	for i := range active {
		activeIDs[active[i].PublicID] = true
	}
	for _, publicID := range publicIDs {
		if !activeIDs[publicID] {
			// Unknown and soft-deleted UUIDs share the existing route 404.
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
	}
	if len(publicIDs) != len(activeIDs) {
		writeError(w, http.StatusBadRequest, "route_ids must include every active route")
		return
	}

	routeIDs := make([]int64, len(publicIDs))
	for i, publicID := range publicIDs {
		id, err := s.routeStore.ResolveRouteID(r.Context(), publicID)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "reorder routes failed", "error", err, "id", publicID)
			writeError(w, http.StatusInternalServerError, "failed to reorder routes")
			return
		}
		routeIDs[i] = id
	}

	if err := s.routeStore.ReorderRoutes(r.Context(), routeIDs); err != nil {
		s.logger.ErrorContext(r.Context(), "reorder routes failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reorder routes")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseRoutePublicID validates a public route identifier from a request via
// router.ParsePublicRouteID (canonical hyphenated UUIDv4, uppercase hex
// accepted, no numeric alias).
func parseRoutePublicID(raw string) (string, bool) {
	return router.ParsePublicRouteID(raw)
}

// routeRequest is the public write shape of a route. `id` and the child
// identity fields are decoded leniently and discarded: a client may echo back
// the UUID strings it read from a response, but identity is never taken from a
// request body — the path resolves the target route, the server assigns the
// created route's UUID, and the store assigns child row IDs (ARS-004).
type routeRequest struct {
	ID                   json.RawMessage         `json:"id"`
	Name                 string                  `json:"name"`
	Description          string                  `json:"description"`
	EvaluationOrder      int                     `json:"evaluation_order"`
	IsFinal              bool                    `json:"is_final"`
	IsEnabled            bool                    `json:"is_enabled"`
	ConditionComposition string                  `json:"condition_composition"`
	Conditions           []routeConditionRequest `json:"conditions"`
	Actions              []routeActionRequest    `json:"actions"`
	Exclusions           []routeExclusionRequest `json:"exclusions"`
}

type routeConditionRequest struct {
	ID             json.RawMessage          `json:"id"`
	RouteID        json.RawMessage          `json:"route_id"`
	ConditionIndex int                      `json:"condition_index"`
	Field          router.ConditionField    `json:"field"`
	Operator       router.ConditionOperator `json:"operator"`
	Value          string                   `json:"value"`
}

type routeActionRequest struct {
	ID               json.RawMessage `json:"id"`
	RouteID          json.RawMessage `json:"route_id"`
	ActionIndex      int             `json:"action_index"`
	ActionType       string          `json:"action_type"`
	TargetPlaylistID string          `json:"target_playlist_id"`
}

type routeExclusionRequest struct {
	ID             json.RawMessage      `json:"id"`
	RouteID        json.RawMessage      `json:"route_id"`
	ExclusionType  router.ExclusionType `json:"exclusion_type"`
	ExclusionValue string               `json:"exclusion_value"`
}

func decodeRouteRequest(r *http.Request) (*routeRequest, error) {
	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid request body")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if len(name) > 100 {
		return nil, fmt.Errorf("name must be 100 characters or fewer")
	}
	comp := strings.ToUpper(req.ConditionComposition)
	if comp != "AND" && comp != "OR" {
		return nil, fmt.Errorf("condition_composition must be AND or OR")
	}
	req.ConditionComposition = comp
	if req.EvaluationOrder < 1 {
		return nil, fmt.Errorf("evaluation_order must be at least 1")
	}
	if len(req.Conditions) == 0 {
		return nil, fmt.Errorf("at least one condition is required")
	}
	if len(req.Actions) == 0 {
		return nil, fmt.Errorf("at least one action is required")
	}
	for i, c := range req.Conditions {
		if err := validateConditionOperator(c.Field, c.Operator, c.Value); err != nil {
			return nil, fmt.Errorf("condition %d: %w", i, err)
		}
	}
	for i := range req.Actions {
		if err := normalizeRouteAction(&req.Actions[i]); err != nil {
			return nil, fmt.Errorf("action %d: %w", i, err)
		}
	}
	for i, e := range req.Exclusions {
		if err := validateRouteExclusion(e); err != nil {
			return nil, fmt.Errorf("exclusion %d: %w", i, err)
		}
	}
	if err := validateDurationExclusionRange(req.Exclusions); err != nil {
		return nil, err
	}
	return &req, nil
}

// normalizeRouteAction enforces the public action contract (ADR-007, BR-004
// AC8): the action type must be the canonical public value and the target
// playlist must be selected. The action is rewritten to the internal
// representation before persistence.
func normalizeRouteAction(a *routeActionRequest) error {
	internal, err := router.FromPublicActionType(a.ActionType)
	if err != nil {
		return err
	}
	if strings.TrimSpace(a.TargetPlaylistID) == "" {
		return fmt.Errorf("target_playlist_id is required")
	}
	a.ActionType = internal
	return nil
}

// proposalRequest is the public request shape of a dry-run draft
// (`draft_configuration`) or backtest proposed configuration
// (`proposed_configuration`). Route `id` values are decoded leniently so that
// a missing, numeric, malformed, or non-UUIDv4 route identity fails with the
// route-id error contract rather than a JSON type error (ADR-012/ARS-004).
type proposalRequest struct {
	Routes             []proposalRouteRequest `json:"routes"`
	DuplicateThreshold float64                `json:"duplicate_threshold"`
	DuplicateBehavior  string                 `json:"duplicate_behavior"`
}

type proposalRouteRequest struct {
	ID                   json.RawMessage            `json:"id"`
	Name                 string                     `json:"name"`
	Description          string                     `json:"description"`
	EvaluationOrder      int                        `json:"evaluation_order"`
	IsFinal              bool                       `json:"is_final"`
	IsEnabled            bool                       `json:"is_enabled"`
	ConditionComposition string                     `json:"condition_composition"`
	Conditions           []config.SnapshotCondition `json:"conditions"`
	Actions              []config.SnapshotAction    `json:"actions"`
	Exclusions           []config.SnapshotExclusion `json:"exclusions"`
}

// snapshot validates the proposal route identities and converts the request
// into the pipeline's proposal configuration. Per ADR-012 §1 every route id
// must be a canonical UUIDv4 (uppercase hex accepted, canonical lowercase
// stored) and no UUID may repeat within one proposal; display names are
// labels only and duplicate names remain independent. A missing, malformed,
// non-v4, numeric-alias, or duplicate route id is invalid input and is
// reported with the existing `invalid route id` error (ARS-004).
func (req *proposalRequest) snapshot() (*config.ProposalSnapshot, error) {
	snap := &config.ProposalSnapshot{
		Routes:             make([]config.ProposalRoute, len(req.Routes)),
		DuplicateThreshold: req.DuplicateThreshold,
		DuplicateBehavior:  req.DuplicateBehavior,
	}
	seen := make(map[string]bool, len(req.Routes))
	for i, r := range req.Routes {
		var raw string
		if err := json.Unmarshal(r.ID, &raw); err != nil {
			return nil, errors.New("invalid route id")
		}
		canonical, ok := parseRoutePublicID(raw)
		if !ok {
			return nil, errors.New("invalid route id")
		}
		if seen[canonical] {
			return nil, errors.New("invalid route id")
		}
		seen[canonical] = true
		snap.Routes[i] = config.ProposalRoute{
			ID:                   canonical,
			Name:                 r.Name,
			Description:          r.Description,
			EvaluationOrder:      r.EvaluationOrder,
			IsFinal:              r.IsFinal,
			IsEnabled:            r.IsEnabled,
			ConditionComposition: r.ConditionComposition,
			Conditions:           r.Conditions,
			Actions:              r.Actions,
			Exclusions:           r.Exclusions,
		}
	}
	return snap, nil
}

// normalizeProposalActions enforces the public action contract (ADR-007) on a
// dry-run draft or backtest proposed configuration decoded from a public
// request. Every action type must be the canonical public value and is
// rewritten to the internal representation before the snapshot is persisted as
// a configuration version or evaluated, so configuration history and simulated
// executed-action evidence keep the internal key.
func normalizeProposalActions(snap *config.ProposalSnapshot) error {
	for ri := range snap.Routes {
		for ai := range snap.Routes[ri].Actions {
			internal, err := router.FromPublicActionType(snap.Routes[ri].Actions[ai].ActionType)
			if err != nil {
				return fmt.Errorf("route %d action %d: %w", ri, ai, err)
			}
			snap.Routes[ri].Actions[ai].ActionType = internal
		}
	}
	return nil
}

// validateRouteExclusion enforces BR-007 AC9-11: only the supported
// route-scoped exclusion categories are accepted, and every entry must carry a
// usable value. Duration bounds must be bare non-negative integers because the
// engine parses them with Atoi and no trimming.
func validateRouteExclusion(e routeExclusionRequest) error {
	switch e.ExclusionType {
	case router.ExclusionTypeChannel, router.ExclusionTypeVideo,
		router.ExclusionTypeTitle, router.ExclusionTypeWord:
		if strings.TrimSpace(e.ExclusionValue) == "" {
			return fmt.Errorf("%s exclusion value is required", e.ExclusionType)
		}
	case router.ExclusionTypeDurationMin, router.ExclusionTypeDurationMax:
		n, err := strconv.Atoi(e.ExclusionValue)
		if err != nil || n < 0 {
			return fmt.Errorf("%s exclusion value must be a whole number of seconds at or above 0", e.ExclusionType)
		}
	default:
		return fmt.Errorf("exclusion type %q is not supported", e.ExclusionType)
	}
	return nil
}

// validateDurationExclusionRange enforces BR-007 AC10: when one route supplies
// both duration limits, the minimum must not exceed the maximum. The check is
// scoped to a single route's own exclusion list.
func validateDurationExclusionRange(exclusions []routeExclusionRequest) error {
	const absent = -1
	minSec, maxSec := absent, absent
	for _, e := range exclusions {
		n, err := strconv.Atoi(e.ExclusionValue)
		if err != nil {
			continue // per-entry validation already rejected unusable values
		}
		switch e.ExclusionType {
		case router.ExclusionTypeDurationMin:
			minSec = n
		case router.ExclusionTypeDurationMax:
			maxSec = n
		}
	}
	if minSec != absent && maxSec != absent && minSec > maxSec {
		return fmt.Errorf("duration minimum %d must not exceed duration maximum %d", minSec, maxSec)
	}
	return nil
}

// validateConditionOperator rejects operators outside the canonical set for a
// field kind, and validates the single-value encoding of the between operator.
func validateConditionOperator(field router.ConditionField, op router.ConditionOperator, value string) error {
	textOperators := map[router.ConditionOperator]bool{
		router.ConditionOperatorContains:     true,
		router.ConditionOperatorEquals:       true,
		router.ConditionOperatorStartsWith:   true,
		router.ConditionOperatorEndsWith:     true,
		router.ConditionOperatorMatchesRegex: true,
	}
	numericOperators := map[router.ConditionOperator]bool{
		router.ConditionOperatorEquals:      true,
		router.ConditionOperatorGreaterThan: true,
		router.ConditionOperatorLessThan:    true,
		router.ConditionOperatorBetween:     true,
	}

	var allowed map[router.ConditionOperator]bool
	switch field {
	case router.ConditionFieldDurationSeconds:
		allowed = numericOperators
	case router.ConditionFieldTitle, router.ConditionFieldChannelID, router.ConditionFieldChannelName, router.ConditionFieldDescription:
		allowed = textOperators
	default:
		return fmt.Errorf("field %q is not valid", field)
	}
	if !allowed[op] {
		return fmt.Errorf("operator %q is not valid for field %q", op, field)
	}
	if field == router.ConditionFieldDurationSeconds {
		if op == router.ConditionOperatorBetween {
			parts := strings.Split(value, ",")
			if len(parts) != 2 {
				return fmt.Errorf("between value must be \"min,max\"")
			}
			if _, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err != nil {
				return fmt.Errorf("between min value must be a number")
			}
			if _, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err != nil {
				return fmt.Errorf("between max value must be a number")
			}
		} else {
			if _, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err != nil {
				return fmt.Errorf("value must be a number for field %q", field)
			}
		}
	}
	return nil
}

func (req *routeRequest) toRoute() *router.Route {
	conditions := make([]router.RouteCondition, 0, len(req.Conditions))
	for _, c := range req.Conditions {
		conditions = append(conditions, router.RouteCondition{
			ConditionIndex: c.ConditionIndex,
			Field:          c.Field,
			Operator:       c.Operator,
			Value:          c.Value,
		})
	}
	actions := make([]router.RouteAction, 0, len(req.Actions))
	for _, a := range req.Actions {
		actions = append(actions, router.RouteAction{
			ActionIndex:      a.ActionIndex,
			ActionType:       a.ActionType,
			TargetPlaylistID: a.TargetPlaylistID,
		})
	}
	exclusions := make([]router.RouteExclusion, 0, len(req.Exclusions))
	for _, e := range req.Exclusions {
		exclusions = append(exclusions, router.RouteExclusion{
			ExclusionType:  e.ExclusionType,
			ExclusionValue: e.ExclusionValue,
		})
	}
	return &router.Route{
		Name:                 req.Name,
		Description:          req.Description,
		EvaluationOrder:      req.EvaluationOrder,
		IsFinal:              req.IsFinal,
		IsEnabled:            req.IsEnabled,
		ConditionComposition: req.ConditionComposition,
		Conditions:           conditions,
		Actions:              actions,
		Exclusions:           exclusions,
	}
}

// routeConditionResponse is the public representation of a route condition.
// route_id is the route's stable public UUID; the condition row's own `id`
// remains its unchanged non-route identifier (ARS-004).
type routeConditionResponse struct {
	ID             int64                    `json:"id"`
	RouteID        string                   `json:"route_id"`
	ConditionIndex int                      `json:"condition_index"`
	Field          router.ConditionField    `json:"field"`
	Operator       router.ConditionOperator `json:"operator"`
	Value          string                   `json:"value"`
}

// routeActionResponse is the public representation of a route action. It
// always emits the canonical public action type, regardless of the stored
// legacy value (ADR-007), and carries the route's stable public UUID.
type routeActionResponse struct {
	ID               int64  `json:"id"`
	RouteID          string `json:"route_id"`
	ActionIndex      int    `json:"action_index"`
	ActionType       string `json:"action_type"`
	TargetPlaylistID string `json:"target_playlist_id"`
}

// routeExclusionResponse is the public representation of a route exclusion.
type routeExclusionResponse struct {
	ID             int64                `json:"id"`
	RouteID        string               `json:"route_id"`
	ExclusionType  router.ExclusionType `json:"exclusion_type"`
	ExclusionValue string               `json:"exclusion_value"`
}

// routeResponse is the public configuration representation of a route. Its
// `id` is the stable public UUID (ARS-004) and it carries no Route List
// statistics projection (ARS-001).
type routeResponse struct {
	ID                   string                   `json:"id"`
	Name                 string                   `json:"name"`
	Description          string                   `json:"description"`
	EvaluationOrder      int                      `json:"evaluation_order"`
	IsFinal              bool                     `json:"is_final"`
	IsEnabled            bool                     `json:"is_enabled"`
	ConditionComposition string                   `json:"condition_composition"`
	Conditions           []routeConditionResponse `json:"conditions"`
	Actions              []routeActionResponse    `json:"actions"`
	Exclusions           []routeExclusionResponse `json:"exclusions"`
	CreatedAt            string                   `json:"created_at"`
	UpdatedAt            string                   `json:"updated_at"`
	DeletedAt            string                   `json:"deleted_at,omitempty"`
}

// routeListEntry adds the Route List statistics projection to the public
// route configuration shape (ARS-001).
type routeListEntry struct {
	routeResponse
	MatchCount int64 `json:"match_count"`
}

// newRouteResponse projects an internal route onto the public DTO. A route
// without a public UUID cannot be represented: the caller must fail the
// endpoint rather than emit an internal integer or omit identity (ARS-004).
func newRouteResponse(r *router.Route) (routeResponse, error) {
	if r.PublicID == "" {
		return routeResponse{}, fmt.Errorf("route %d has no public identity", r.ID)
	}
	conditions := make([]routeConditionResponse, 0, len(r.Conditions))
	for _, c := range r.Conditions {
		conditions = append(conditions, routeConditionResponse{
			ID:             c.ID,
			RouteID:        r.PublicID,
			ConditionIndex: c.ConditionIndex,
			Field:          c.Field,
			Operator:       c.Operator,
			Value:          c.Value,
		})
	}
	actions := make([]routeActionResponse, 0, len(r.Actions))
	for _, a := range r.Actions {
		actions = append(actions, routeActionResponse{
			ID:               a.ID,
			RouteID:          r.PublicID,
			ActionIndex:      a.ActionIndex,
			ActionType:       router.ToPublicActionType(a.ActionType),
			TargetPlaylistID: a.TargetPlaylistID,
		})
	}
	exclusions := make([]routeExclusionResponse, 0, len(r.Exclusions))
	for _, e := range r.Exclusions {
		exclusions = append(exclusions, routeExclusionResponse{
			ID:             e.ID,
			RouteID:        r.PublicID,
			ExclusionType:  e.ExclusionType,
			ExclusionValue: e.ExclusionValue,
		})
	}
	return routeResponse{
		ID:                   r.PublicID,
		Name:                 r.Name,
		Description:          r.Description,
		EvaluationOrder:      r.EvaluationOrder,
		IsFinal:              r.IsFinal,
		IsEnabled:            r.IsEnabled,
		ConditionComposition: r.ConditionComposition,
		Conditions:           conditions,
		Actions:              actions,
		Exclusions:           exclusions,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		DeletedAt:            r.DeletedAt,
	}, nil
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
