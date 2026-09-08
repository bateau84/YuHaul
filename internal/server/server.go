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
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/pipeline"
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

// Server serves the YuHaul REST API and SSE endpoints.
type Server struct {
	pipeline         Pipeline
	broadcast        *pipeline.Broadcaster
	configHandler    ConfigHandler
	analyticsHandler AnalyticsHandler
	advisoryHandler  AnalyticsHandler
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

// NewServer returns an API server.
func NewServer(pipeline Pipeline, broadcast *pipeline.Broadcaster, opts ...Option) *Server {
	s := &Server{
		pipeline:         pipeline,
		broadcast:        broadcast,
		configHandler:    &noopHandler{},
		analyticsHandler: &noopHandler{},
		advisoryHandler:  &noopHandler{},
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

// Handler returns the HTTP handler for the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("POST /api/runs/dry-run", s.handleDryRun)
	mux.HandleFunc("POST /api/runs/replay", s.handleReplay)
	mux.HandleFunc("POST /api/backtest", s.handleBacktest)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.handleRunStream)

	// Configuration-version endpoints are served by a dedicated handler.
	mux.Handle("/api/config/", s.configHandler.Handler())
	mux.Handle("/api/settings/", s.configHandler.Handler())

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
