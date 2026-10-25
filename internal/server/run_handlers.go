package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

func nullableOutcome(video state.Video) *string {
	if !video.FinalOutcomeKnown {
		return nil
	}
	outcome := string(video.FinalOutcome)
	return &outcome
}

// videoListItem is the response shape for a video in the run video list.
type videoListItem struct {
	ID              int64                  `json:"id"`
	VideoID         string                 `json:"video_id"`
	ChannelID       string                 `json:"channel_id"`
	DiscoveredAt    time.Time              `json:"discovered_at"`
	ProcessingState string                 `json:"processing_state"`
	FinalOutcome    *string                `json:"final_outcome"`
	DeferredReason  *string                `json:"deferred_reason,omitempty"`
	ErrorMessage    *string                `json:"error_message,omitempty"`
	CompletedAt     *time.Time             `json:"completed_at,omitempty"`
	RouteDecisions  []routeDecisionSummary `json:"route_decisions"`
}

// routeDecisionSummary is a lightweight representation of a route decision for
// list responses. RouteID is the stable public route UUID (ARS-004); the
// internal integer route key never appears on the wire.
type routeDecisionSummary struct {
	RouteID         string  `json:"route_id"`
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
// action_type uses the public API vocabulary (ADR-007), independent of the
// stored action representation.
type executedActionDetail struct {
	ActionType       string     `json:"action_type"`
	TargetPlaylistID string     `json:"target_playlist_id"`
	Outcome          string     `json:"outcome"`
	ErrorMessage     *string    `json:"error_message,omitempty"`
	ExecutedAt       *time.Time `json:"executed_at,omitempty"`
}

// routeDecisionDetail is the full response shape for a route decision, used in
// the decision detail endpoint. RouteID is the stable public route UUID
// (ARS-004).
type routeDecisionDetail struct {
	RouteID         string                 `json:"route_id"`
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
	FinalOutcome    *string               `json:"final_outcome"`
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

	run, err := s.runVideoStore.GetRun(r.Context(), runID)
	if err != nil {
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

	// Optional outcome-category filter (BR-032 AC5). An absent or empty
	// parameter returns every video; a known final-outcome category narrows
	// both the returned videos and the pagination totals to that category.
	// Unknown categories are rejected rather than silently ignored, so the
	// client never receives an unfiltered page while believing it is filtered.
	outcomeFilter := state.VideoFinalOutcome(r.URL.Query().Get("outcome"))
	if outcomeFilter != "" && !outcomeFilter.Valid() {
		writeError(w, http.StatusBadRequest, "invalid outcome filter")
		return
	}

	videos, total, err := s.runVideoStore.ListRunVideos(r.Context(), runID, pageSize, offset, outcomeFilter)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list run videos failed", "error", err, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to list run videos")
		return
	}

	evidence, err := s.runRouteEvidence(r.Context(), run)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "resolve run route evidence failed", "error", err, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to resolve route identity")
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
		item, err := toVideoListItem(v, decisions, evidence)
		if err != nil {
			// A decision whose route has no identity mapping fails the
			// whole response; it is never serialized with an integer or
			// a name-only label (ARS-004 failure behavior).
			s.logger.ErrorContext(r.Context(), "serialize route decisions failed", "error", err, "video_id", v.ID, "run_id", runID)
			writeError(w, http.StatusInternalServerError, "failed to resolve route identity")
			return
		}
		items = append(items, item)
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

	run, err := s.runVideoStore.GetRun(r.Context(), runID)
	if err != nil {
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

	evidence, err := s.runRouteEvidence(r.Context(), run)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "resolve run route evidence failed", "error", err, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to resolve route identity")
		return
	}

	detail, err := s.buildVideoDetail(r.Context(), runID, *video, evidence)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "build video detail failed", "error", err, "video_id", video.ID, "run_id", runID)
		writeError(w, http.StatusInternalServerError, "failed to build video detail")
		return
	}

	writeJSON(w, http.StatusOK, videoDetailResponse{Video: detail})
}

// runRouteEvidence resolves the public route identity and the display label for
// every route referenced by one run's decision history (ARS-001/ARS-004).
//
// Identity comes from the route_identity registry, which retains mappings for
// soft-deleted routes and snapshot-only historical keys, so a decision always
// resolves to the same stable UUID regardless of a later rename or deletion.
// A route key with no registry mapping is an integrity error: the endpoint
// fails rather than emitting an integer or a name-only label (ARS-004
// invariant 6).
//
// Labels come from the run's immutable ConfigVersion snapshot (OC-003) — the
// configuration that run actually evaluated — so historical renames and
// proposal-only routes keep their evaluated name. The current persisted route
// name, including soft-deleted rows, fills only routes the snapshot does not
// cover (legacy runs with no usable snapshot, OC-003 incomplete context).
type runRouteEvidence struct {
	publicIDs map[int64]string
	labels    map[int64]string
}

// publicID returns the stable public UUID for an internal route key.
func (e *runRouteEvidence) publicID(routeID int64) (string, error) {
	id, ok := e.publicIDs[routeID]
	if !ok || id == "" {
		return "", fmt.Errorf("route %d has no route identity mapping", routeID)
	}
	return id, nil
}

// label returns the route's display name, or "" when no snapshot or persisted
// name can recover it. The UUID still carries identity; a fabricated
// name-derived label is never substituted.
func (e *runRouteEvidence) label(routeID int64) string {
	return e.labels[routeID]
}

func (s *Server) runRouteEvidence(ctx context.Context, run *state.Run) (*runRouteEvidence, error) {
	publicIDs, err := s.routeStore.ListRouteIdentities(ctx)
	if err != nil {
		return nil, fmt.Errorf("load route identities: %w", err)
	}

	currentNames, err := s.routeStore.ListRouteNamesIncludingDeleted(ctx)
	if err != nil {
		return nil, fmt.Errorf("load route names: %w", err)
	}

	snapshotNames, err := s.snapshotRouteLabels(ctx, run)
	if err != nil {
		return nil, err
	}

	// Snapshot labels win; current persisted names fill the gaps.
	labels := snapshotNames
	if labels == nil {
		labels = make(map[int64]string, len(currentNames))
	}
	for id, name := range currentNames {
		if _, ok := labels[id]; !ok {
			labels[id] = name
		}
	}

	return &runRouteEvidence{publicIDs: publicIDs, labels: labels}, nil
}

// snapshotRouteLabels returns the route-id to name map from the run's linked
// ConfigVersion snapshot. It returns a nil map with no error when the run has
// no linked version or the stored snapshot is missing or undecodable: OC-003
// treats that as incomplete configuration context, and the caller falls back
// to current persisted names instead of failing run inspection or fabricating
// labels. A failure to read the version row is a real error, not incomplete
// context.
func (s *Server) snapshotRouteLabels(ctx context.Context, run *state.Run) (map[int64]string, error) {
	if run.ConfigVersionID == nil {
		return nil, nil
	}
	cv, err := s.configVersionStore.Get(ctx, *run.ConfigVersionID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) || isSnapshotDecodeError(err) {
			s.logger.WarnContext(ctx, "run configuration snapshot unavailable; using current route names",
				"run_id", run.ID, "config_version_id", *run.ConfigVersionID, "error", err)
			return nil, nil
		}
		return nil, fmt.Errorf("load run configuration version %d: %w", *run.ConfigVersionID, err)
	}

	labels := make(map[int64]string, len(cv.Snapshot.Routes))
	for _, r := range cv.Snapshot.Routes {
		labels[r.ID] = r.Name
	}
	return labels, nil
}

// isSnapshotDecodeError reports whether err is a JSON decode failure from
// loading a stored ConfigVersion snapshot, which OC-003 treats as incomplete
// configuration context rather than an endpoint failure.
func isSnapshotDecodeError(err error) bool {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &syntaxErr) || errors.As(err, &typeErr)
}

func (s *Server) buildVideoDetail(ctx context.Context, runID int64, video state.Video,
	evidence *runRouteEvidence) (videoDetail, error) {

	decisions, err := s.runVideoStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, runID)
	if err != nil {
		return videoDetail{}, fmt.Errorf("loading route decisions: %w", err)
	}
	actions, err := s.runVideoStore.GetExecutedActionsForVideoInRun(ctx, video.ID, runID)
	if err != nil {
		return videoDetail{}, fmt.Errorf("loading executed actions: %w", err)
	}

	decisionDetails := make([]routeDecisionDetail, 0, len(decisions))
	for _, d := range decisions {
		audits, err := s.runVideoStore.GetConditionAuditsForDecision(ctx, d.ID)
		if err != nil {
			return videoDetail{}, fmt.Errorf("loading condition audits for decision %d: %w", d.ID, err)
		}
		detail, err := toRouteDecisionDetail(d, audits, actions, evidence)
		if err != nil {
			return videoDetail{}, err
		}
		decisionDetails = append(decisionDetails, detail)
	}

	return videoDetail{
		ID:              video.ID,
		VideoID:         video.VideoID,
		ChannelID:       video.ChannelID,
		DiscoveredAt:    video.DiscoveredAt,
		ProcessingState: string(video.ProcessingState),
		FinalOutcome:    nullableOutcome(video),
		DeferredReason:  video.DeferredReason,
		ErrorMessage:    video.ErrorMessage,
		CompletedAt:     video.CompletedAt,
		RouteDecisions:  decisionDetails,
	}, nil
}

func toVideoListItem(video state.Video, decisions []state.RouteDecision,
	evidence *runRouteEvidence) (videoListItem, error) {

	summaries := make([]routeDecisionSummary, 0, len(decisions))
	for _, d := range decisions {
		publicID, err := evidence.publicID(d.RouteID)
		if err != nil {
			return videoListItem{}, err
		}
		summaries = append(summaries, routeDecisionSummary{
			RouteID:         publicID,
			RouteName:       evidence.label(d.RouteID),
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
		FinalOutcome:    nullableOutcome(video),
		DeferredReason:  video.DeferredReason,
		ErrorMessage:    video.ErrorMessage,
		CompletedAt:     video.CompletedAt,
		RouteDecisions:  summaries,
	}, nil
}

func toRouteDecisionDetail(d state.RouteDecision, audits []state.ConditionAudit,
	actions []state.ExecutedAction, evidence *runRouteEvidence) (routeDecisionDetail, error) {

	publicID, err := evidence.publicID(d.RouteID)
	if err != nil {
		return routeDecisionDetail{}, err
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
			ActionType:       router.ToPublicActionType(a.ActionType),
			TargetPlaylistID: a.TargetPlaylistID,
			Outcome:          string(a.Outcome),
			ErrorMessage:     a.ErrorMessage,
			ExecutedAt:       a.ExecutedAt,
		})
	}

	return routeDecisionDetail{
		RouteID:         publicID,
		RouteName:       evidence.label(d.RouteID),
		EvaluationOrder: d.EvaluationOrder,
		Decision:        string(d.Decision),
		SkipReason:      skipReasonString(d.SkipReason),
		RejectionReason: d.RejectionReason,
		ConditionAudits: auditDetails,
		ExecutedActions: actionDetails,
	}, nil
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

// compile-time interface check: config.Store implements ConfigVersionStore.
var _ ConfigVersionStore = (*config.Store)(nil)
