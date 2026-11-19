package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/videosearch"
)

// VideoSearcher answers the two video-search reads (video-search ARS-001). It is
// implemented by *videosearch.Service.
type VideoSearcher interface {
	Search(ctx context.Context, raw string, p videosearch.Paging) (videosearch.SearchResult, error)
	View(ctx context.Context, videoID string) (videosearch.VideoView, error)
}

type noopVideoSearcher struct{}

func (noopVideoSearcher) Search(context.Context, string, videosearch.Paging) (videosearch.SearchResult, error) {
	return videosearch.SearchResult{}, errors.New("video search not configured")
}

func (noopVideoSearcher) View(context.Context, string) (videosearch.VideoView, error) {
	return videosearch.VideoView{}, errors.New("video search not configured")
}

// WithVideoSearch enables GET /api/videos/search and GET /api/videos/{videoId}.
func WithVideoSearch(v VideoSearcher) Option {
	return func(s *Server) {
		if v != nil {
			s.videoSearch = v
		}
	}
}

// handleVideoSearch is GET /api/videos/search (ARS-001 §2). Logs carry the kind,
// path (indexed or scan), counts, stage durations and total duration only:
// never q, a title, a channel name or a cursor.
func (s *Server) handleVideoSearch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	start := time.Now()
	q := r.URL.Query()
	p := videosearch.Paging{
		Cursor: q.Get("cursor"), HasCursor: q.Has("cursor"),
		Page: q.Get("page"), HasPage: q.Has("page"),
	}
	res, err := s.videoSearch.Search(r.Context(), q.Get("q"), p)
	switch {
	case errors.Is(err, videosearch.ErrInvalidPage):
		writeError(w, http.StatusBadRequest, "invalid_page")
		return
	case errors.Is(err, videosearch.ErrInvalidCursor):
		writeError(w, http.StatusBadRequest, "invalid_cursor")
		return
	case err != nil:
		if s.clientAborted(r.Context(), "video search failed", err) {
			return
		}
		s.logger.ErrorContext(r.Context(), "video search failed",
			"diagnostic", diag.Encode(err, "unknown", "video_search"))
		writeError(w, http.StatusInternalServerError, "search_failed")
		return
	}
	encodeStart := time.Now()
	writeJSON(w, http.StatusOK, res)
	encode := time.Since(encodeStart)

	attrs := []any{"kind", res.Kind}
	if st := res.Stats; st.Path != "" { // a name search read the candidates
		attrs = append(attrs, "path", string(st.Path),
			"candidates", st.Candidates, "matches", st.Matches,
			slog.Group("stages", // time.Duration: nanoseconds in JSON logs (ARS-001 §9.2)
				"begin", st.Stages.Begin, "candidates", st.Stages.Candidates,
				"recheck", st.Stages.Recheck, "sort", st.Stages.Sort,
				"project", st.Stages.Project, "encode", encode))
	}
	attrs = append(attrs, "duration_ms", time.Since(start).Milliseconds())
	s.logger.InfoContext(r.Context(), "video search", attrs...)
}

// handleGetVideo is GET /api/videos/{videoId} (ARS-001 §2).
func (s *Server) handleGetVideo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := s.videoSearch.View(r.Context(), r.PathValue("videoId"))
	switch {
	case errors.Is(err, videosearch.ErrInvalidVideoID):
		writeError(w, http.StatusBadRequest, "invalid_video_id")
		return
	case errors.Is(err, videosearch.ErrNotFound):
		writeError(w, http.StatusNotFound, "video_not_found")
		return
	case err != nil:
		if s.clientAborted(r.Context(), "video view failed", err) {
			return
		}
		s.logger.ErrorContext(r.Context(), "video view failed",
			"diagnostic", diag.Encode(err, "unknown", "video_search"))
		writeError(w, http.StatusInternalServerError, "video_view_failed")
		return
	}
	writeJSON(w, http.StatusOK, view)
}
