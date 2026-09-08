// Package server exposes the YuHaul HTTP API, including manual run triggers and
// SSE run-progress streams.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/state"
)

// Pipeline is the subset of the pipeline package used by the HTTP server.
type Pipeline interface {
	StartRun(ctx context.Context, runType state.RunType) (*state.Run, error)
	Execute(ctx context.Context, runID int64)
}

// Server serves the YuHaul REST API and SSE endpoints.
type Server struct {
	pipeline  Pipeline
	broadcast *pipeline.Broadcaster
	logger    *slog.Logger
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

// NewServer returns an API server.
func NewServer(pipeline Pipeline, broadcast *pipeline.Broadcaster, opts ...Option) *Server {
	s := &Server{
		pipeline:  pipeline,
		broadcast: broadcast,
		logger:    slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Handler returns the HTTP handler for the API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/runs", s.handleStartRun)
	mux.HandleFunc("GET /api/runs/{id}/stream", s.handleRunStream)
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
