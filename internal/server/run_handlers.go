package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// videoListItem is the response shape for a video in the run video list.
type videoListItem struct {
	ID              int64                  `json:"id"`
	VideoID         string                 `json:"video_id"`
	ChannelID       string                 `json:"channel_id"`
	DiscoveredAt    time.Time              `json:"discovered_at"`
	ProcessingState string                 `json:"processing_state"`
	FinalOutcome    string                 `json:"final_outcome"`
	DeferredReason  *string                `json:"deferred_reason,omitempty"`
	ErrorMessage    *string                `json:"error_message,omitempty"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`
	RouteDecisions  []routeDecisionSummary `json:"route_decisions"`
}

// routeDecisionSummary is a lightweight representation of a route decision for
// list responses.
type routeDecisionSummary struct {
	RouteID         int64   `json:"route_id"`
	RouteName       string  `json:"route_name"`
	EvaluationOrder int     `json:"evaluation_order"`
	Decision        string  `json:"decision"`
	SkipReason      *string `json:"skip_reason,omitempty"`
	RejectionReason *string `json:"rejection_reason,omitempty"`
}

// paginationMeta describes the pagination state for list responses.
type paginationMeta struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

// videoListResponse is the response body for GET /api/runs/:id/videos.
type videoListResponse struct {
	Videos     []videoListItem `json:"videos"`
	Pagination paginationMeta  `json:"pagination"`
}

// conditionAuditDetail is the response shape for a condition audit entry.
type conditionAuditDetail struct {
	ConditionIndex int     `json:"condition_index"`
	Field          string  `json:"field"`
	Operator       string  `json:"operator"`
	ExpectedValue  string  `json:"expected_value"`
	ActualValue    *string `json:"actual_value,omitempty"`
	Matched        bool    `json:"matched"`
}

// executedActionDetail is the response shape for an executed action entry.
type executedActionDetail struct {
	ActionType       string     `json:"action_type"`
	TargetPlaylistID string     `json:"target_playlist_id"`
	Outcome          string     `json:"outcome"`
	ErrorMessage     *string    `json:"error_message,omitempty"`
	ExecutedAt       *time.Time `json:"executed_at,omitempty"`
}

// routeDecisionDetail is the full response shape for a route decision, used in
// the decision detail endpoint.
type routeDecisionDetail struct {
	RouteID         int64                  `json:"route_id"`
	RouteName       string                 `json:"route_name"`
	EvaluationOrder int                    `json:"evaluation_order"`
	Decision        string                 `json:"decision"`
	SkipReason      *string                `json:"skip_reason,omitempty"`
	RejectionReason *string                `json:"rejection_reason,omitempty"`
	ConditionAudits []conditionAuditDetail `json:"condition_audits"`
	ExecutedActions []executedActionDetail `json:"executed_actions"`
}

// videoDetail is the response shape for a single video in the decision detail
// endpoint.
type videoDetail struct {
	ID              int64                 `json:"id"`
	VideoID         string                `json:"video_id"`
	ChannelID       string                `json:"channel_id"`
	DiscoveredAt    time.Time             `json:"discovered_at"`
	ProcessingState string                `json:"processing_state"`
	FinalOutcome    string                `json:"final_outcome"`
	DeferredReason  *string               `json:"deferred_reason,omitempty"`
	ErrorMessage    *string               `json:"error_message,omitempty"`
	CompletedAt     *time.Time            `json:"completed_at,omitempty"`
	RouteDecisions  []routeDecisionDetail `json:"route_decisions"`
}

// videoDetailResponse is the response body for GET /api/runs/:id/videos/:videoId.
type videoDetailResponse struct {
	Video videoDetail `json:"video"`
}

func (s *Server) handleListRunVideos(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	if _, err := s.runVideoStore.GetRun(r.Context(), runID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get run failed", "error", err, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}

	page, pageSize := parsePagination(r, defaultPageSize, maxPageSize)
	offset := (page - 1) * pageSize

	videos, total, err := s.runVideoStore.ListRunVideos(r.Context(), runID, pageSize, offset)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list run videos failed", "error", err, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to list run videos")
		return
	}

	routeNames, err := s.routeNameMap(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load route names failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load route names")
		return
	}

	items := make([]videoListItem, 0, len(videos))
	for _, v := range videos {
		decisions, err := s.runVideoStore.GetRouteDecisionsForVideoInRun(r.Context(), v.ID, runID)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "load route decisions failed", "error", err, "video_id", v.ID, "run_id", runID)
			writeError(w, http.StatusInternalServerError, "failed to load route decisions")
			return
		}
		items = append(items, toVideoListItem(v, decisions, routeNames))
	}

	resp := videoListResponse{
		Videos: items,
		Pagination: paginationMeta{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: int(math.Ceil(float64(total) / float64(pageSize))),
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetRunVideo(w http.ResponseWriter, r *http.Request) {
	runID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	videoID := r.PathValue("videoId")
	if videoID == "" {
		writeError(w, http.StatusBadRequest, "invalid video id")
		return
	}

	if _, err := s.runVideoStore.GetRun(r.Context(), runID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get run failed", "error", err, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}

	video, err := s.runVideoStore.GetVideoByExternalIDInRun(r.Context(), runID, videoID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "video not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get run video failed", "error", err, "run_id", runID, "video_id", videoID)
		writeError(w, http.StatusInternalServerError, "failed to load video")
		return
	}

	routeNames, err := s.routeNameMap(r.Context())
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load route names failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load route names")
		return
	}

	detail, err := s.buildVideoDetail(r.Context(), runID, *video, routeNames)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "build video detail failed", "error", err, "video_id", video.ID, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to build video detail")
		return
	}

	writeJSON(w, http.StatusOK, videoDetailResponse{Video: detail})
}

// routeNameMap loads route names into a map keyed by route ID. The run video
// endpoints use this to make decision summaries human-readable without
// requiring the client to fetch routes separately.
func (s *Server) routeNameMap(ctx context.Context) (map[int64]string, error) {
	routes, err := s.routeStore.ListRoutes(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing routes: %w", err)
	}
	m := make(map[int64]string, len(routes))
	for _, r := range routes {
		m[r.ID] = r.Name
	}
	return m, nil
}

func (s *Server) buildVideoDetail(ctx context.Context, runID int64, video state.Video,
	routeNames map[int64]string) (videoDetail, error) {

	decisions, err := s.runVideoStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, runID)
	if err != nil {
		return videoDetail{}, fmt.Errorf("loading route decisions: %w", err)
	}

	decisionDetails := make([]routeDecisionDetail, 0, len(decisions))
	for _, d := range decisions {
		audits, err := s.runVideoStore.GetConditionAuditsForDecision(ctx, d.ID)
		if err != nil {
			return videoDetail{}, fmt.Errorf("loading condition audits for decision %d: %w", d.ID, err)
		}
		actions, err := s.runVideoStore.GetExecutedActionsForVideoInRun(ctx, video.ID, runID)
		if err != nil {
			return videoDetail{}, fmt.Errorf("loading executed actions: %w", err)
		}
		decisionDetails = append(decisionDetails, toRouteDecisionDetail(d, audits, actions, routeNames))
	}

	return videoDetail{
		ID:              video.ID,
		VideoID:         video.VideoID,
		ChannelID:       video.ChannelID,
		DiscoveredAt:    video.DiscoveredAt,
		ProcessingState: string(video.ProcessingState),
		FinalOutcome:    string(video.FinalOutcome),
		DeferredReason:  video.DeferredReason,
		ErrorMessage:    video.ErrorMessage,
		CompletedAt:     video.CompletedAt,
		RouteDecisions:  decisionDetails,
	}, nil
}

func toVideoListItem(video state.Video, decisions []state.RouteDecision,
	routeNames map[int64]string) videoListItem {

	summaries := make([]routeDecisionSummary, 0, len(decisions))
	for _, d := range decisions {
		name := routeNames[d.RouteID]
		if name == "" {
			name = fmt.Sprintf("route-%d", d.RouteID)
		}
		summaries = append(summaries, routeDecisionSummary{
			RouteID:         d.RouteID,
			RouteName:       name,
			EvaluationOrder: d.EvaluationOrder,
			Decision:        string(d.Decision),
			SkipReason:      skipReasonString(d.SkipReason),
			RejectionReason: d.RejectionReason,
		})
	}
	return videoListItem{
		ID:              video.ID,
		VideoID:         video.VideoID,
		ChannelID:       video.ChannelID,
		DiscoveredAt:    video.DiscoveredAt,
		ProcessingState: string(video.ProcessingState),
		FinalOutcome:    string(video.FinalOutcome),
		DeferredReason:  video.DeferredReason,
		ErrorMessage:    video.ErrorMessage,
		CompletedAt:     video.CompletedAt,
		RouteDecisions:  summaries,
	}
}

func toRouteDecisionDetail(d state.RouteDecision, audits []state.ConditionAudit,
	actions []state.ExecutedAction, routeNames map[int64]string) routeDecisionDetail {

	name := routeNames[d.RouteID]
	if name == "" {
		name = fmt.Sprintf("route-%d", d.RouteID)
	}

	auditDetails := make([]conditionAuditDetail, 0, len(audits))
	for _, a := range audits {
		auditDetails = append(auditDetails, conditionAuditDetail{
			ConditionIndex: a.ConditionIndex,
			Field:          a.Field,
			Operator:       a.Operator,
			ExpectedValue:  a.ExpectedValue,
			ActualValue:    a.ActualValue,
			Matched:        a.Matched,
		})
	}

	actionDetails := make([]executedActionDetail, 0, len(actions))
	for _, a := range actions {
		if a.RouteDecisionID != d.ID {
			continue
		}
		actionDetails = append(actionDetails, executedActionDetail{
			ActionType:       a.ActionType,
			TargetPlaylistID: a.TargetPlaylistID,
			Outcome:          string(a.Outcome),
			ErrorMessage:     a.ErrorMessage,
			ExecutedAt:       a.ExecutedAt,
		})
	}

	return routeDecisionDetail{
		RouteID:         d.RouteID,
		RouteName:       name,
		EvaluationOrder: d.EvaluationOrder,
		Decision:        string(d.Decision),
		SkipReason:      skipReasonString(d.SkipReason),
		RejectionReason: d.RejectionReason,
		ConditionAudits: auditDetails,
		ExecutedActions: actionDetails,
	}
}

func skipReasonString(sr *state.SkipReason) *string {
	if sr == nil {
		return nil
	}
	s := string(*sr)
	return &s
}

func parsePagination(r *http.Request, defaultSize, maxSize int) (page, pageSize int) {
	page = 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	pageSize = defaultSize
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 {
			pageSize = n
			if pageSize > maxSize {
				pageSize = maxSize
			}
		}
	}
	return page, pageSize
}

// compile-time interface check: state.Store implements RunVideoStore.
var _ RunVideoStore = (*state.Store)(nil)

// compile-time interface check: router.Store implements the subset of RouteStore
// used by run handlers.
var _ RouteStore = (*router.Store)(nil)
