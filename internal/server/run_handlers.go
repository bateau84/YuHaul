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
	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/metadata"
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

// safeErrorMessage admits stored canonical or exact legacy diagnostics. It
// returns normalized text, withholds rejected legacy prose, and marks generic
// unknown_error summaries unavailable without discarding their safe phase and
// operation (ARD-001).
func safeErrorMessage(msg *string) (*string, bool) {
	if msg == nil || *msg == "" {
		return nil, false
	}
	value, unavailable := diag.AdmitErrorText(*msg)
	if value == "" {
		return nil, unavailable
	}
	return &value, unavailable
}

func safeDispositionReason(value *string, field string) (*string, bool) {
	if value == nil || *value == "" {
		return nil, false
	}
	admitted, ok := diag.AdmitDisposition(*value, field)
	if !ok {
		return nil, true
	}
	return &admitted, false
}

// videoListItem is the response shape for a video in the run video list.
type videoListItem struct {
	ID                        int64     `json:"id"`
	VideoID                   string    `json:"video_id"`
	ChannelID                 string    `json:"channel_id"`
	DiscoveredAt              time.Time `json:"discovered_at"`
	ProcessingState           string    `json:"processing_state"`
	FinalOutcome              *string   `json:"final_outcome"`
	DeferredReason            *string   `json:"deferred_reason,omitempty"`
	DeferredReasonUnavailable bool      `json:"deferred_reason_unavailable,omitempty"`
	ErrorMessage              *string   `json:"error_message,omitempty"`
	// ErrorMessageUnavailable is set when a stored error message exists but
	// fails the safe-diagnostic vetting: the legacy text is withheld, never
	// serialized raw (ARD-001).
	ErrorMessageUnavailable bool                       `json:"error_message_unavailable,omitempty"`
	CompletedAt             *time.Time                 `json:"completed_at,omitempty"`
	RouteDecisions          []routeDecisionSummary     `json:"route_decisions"`
	CurrentLocalMetadata    *metadata.LocalDisplay     `json:"current_local_metadata"`
	MetadataContextStatus   string                     `json:"metadata_context_status"`
	UnresolvedMutations     []unresolvedMutationDetail `json:"unresolved_mutations"`
}

type unresolvedMutationDetail struct {
	ActionType       string    `json:"action_type"`
	TargetPlaylistID string    `json:"target_playlist_id"`
	PreparedAt       time.Time `json:"prepared_at"`
	ApplicationState string    `json:"application_state"`
}

// routeDecisionSummary is a lightweight representation of a route decision for
// list responses. RouteID is the stable public route UUID (ARS-004); the
// internal integer route key never appears on the wire.
type routeDecisionSummary struct {
	RouteID         string           `json:"route_id"`
	RouteName       string           `json:"route_name"`
	RouteNameSource string           `json:"route_name_source"`
	EvaluationOrder int              `json:"evaluation_order"`
	Decision        string           `json:"decision"`
	SkipReason      *string          `json:"skip_reason,omitempty"`
	RejectionReason *string          `json:"rejection_reason,omitempty"`
	CausalExclusion *causalExclusion `json:"causal_exclusion"`
	// FailedCondition is list-only. It is nil unless the decision did not
	// match and the run's snapshot can name the first failed condition.
	FailedCondition *failedCondition `json:"failed_condition,omitempty"`
}

// failedCondition summarizes why a route did not match, for the compact route
// line on the Run Detail list. Field, operator, value, composition and count
// come only from the run's immutable configuration snapshot; the condition
// audit contributes only which conditions failed. It never carries the video's
// compared value.
type failedCondition struct {
	// First failed condition, in recorded condition order.
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
	// Composition is "AND" or "OR", as the route evaluated it.
	Composition string `json:"composition"`
	// ConditionCount is the number of conditions configured on the route.
	ConditionCount int `json:"condition_count"`
	// FurtherFailed counts failed conditions after the first one.
	FurtherFailed int `json:"further_failed"`
}

type causalExclusion struct {
	Status                string  `json:"status"`
	ConfigVersionID       *int64  `json:"config_version_id"`
	ExclusionIndex        *int    `json:"exclusion_index"`
	SemanticsVersion      *int    `json:"semantics_version"`
	ExclusionType         *string `json:"exclusion_type"`
	Field                 *string `json:"field"`
	Comparison            *string `json:"comparison"`
	ConfiguredValue       *string `json:"configured_value"`
	ConfiguredValueStatus string  `json:"configured_value_status"`
	Matched               *bool   `json:"matched"`
	ComparedInputStatus   string  `json:"compared_input_status"`
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
// stored action representation. outcome_reason explains the recorded outcome
// (for example an already_completed no-attempt) and is optional evidence, not
// an outcome category (ARD-001).
type executedActionDetail struct {
	ActionType               string  `json:"action_type"`
	TargetPlaylistID         string  `json:"target_playlist_id"`
	Outcome                  string  `json:"outcome"`
	OutcomeReason            *string `json:"outcome_reason,omitempty"`
	OutcomeReasonUnavailable bool    `json:"outcome_reason_unavailable,omitempty"`
	ErrorMessage             *string `json:"error_message,omitempty"`
	// ErrorMessageUnavailable marks withheld unsafe legacy text (ARD-001).
	ErrorMessageUnavailable bool       `json:"error_message_unavailable,omitempty"`
	ExecutedAt              *time.Time `json:"executed_at,omitempty"`
}

// routeDecisionDetail is the full response shape for a route decision, used in
// the decision detail endpoint. RouteID is the stable public route UUID
// (ARS-004).
type routeDecisionDetail struct {
	RouteID         string                 `json:"route_id"`
	RouteName       string                 `json:"route_name"`
	RouteNameSource string                 `json:"route_name_source"`
	EvaluationOrder int                    `json:"evaluation_order"`
	Decision        string                 `json:"decision"`
	SkipReason      *string                `json:"skip_reason,omitempty"`
	RejectionReason *string                `json:"rejection_reason,omitempty"`
	ConditionAudits []conditionAuditDetail `json:"condition_audits"`
	ExecutedActions []executedActionDetail `json:"executed_actions"`
	CausalExclusion *causalExclusion       `json:"causal_exclusion"`
}

// videoDetail is the response shape for a single video in the decision detail
// endpoint.
type videoDetail struct {
	ID                        int64     `json:"id"`
	VideoID                   string    `json:"video_id"`
	ChannelID                 string    `json:"channel_id"`
	DiscoveredAt              time.Time `json:"discovered_at"`
	ProcessingState           string    `json:"processing_state"`
	FinalOutcome              *string   `json:"final_outcome"`
	DeferredReason            *string   `json:"deferred_reason,omitempty"`
	DeferredReasonUnavailable bool      `json:"deferred_reason_unavailable,omitempty"`
	ErrorMessage              *string   `json:"error_message,omitempty"`
	// ErrorMessageUnavailable marks withheld unsafe legacy text (ARD-001).
	ErrorMessageUnavailable bool                       `json:"error_message_unavailable,omitempty"`
	CompletedAt             *time.Time                 `json:"completed_at,omitempty"`
	RouteDecisions          []routeDecisionDetail      `json:"route_decisions"`
	CurrentLocalMetadata    *metadata.LocalDisplay     `json:"current_local_metadata"`
	MetadataContextStatus   string                     `json:"metadata_context_status"`
	UnresolvedMutations     []unresolvedMutationDetail `json:"unresolved_mutations"`
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
		s.logger.ErrorContext(r.Context(), "get run failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
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
		s.logger.ErrorContext(r.Context(), "list run videos failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to list run videos")
		return
	}
	internalIDs := make([]int64, len(videos))
	for i := range videos {
		internalIDs[i] = videos[i].ID
	}
	mutations, err := s.runVideoStore.ListUnresolvedMutations(r.Context(), runID, internalIDs)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list unresolved mutations failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to list run videos")
		return
	}

	evidence, err := s.runRouteEvidence(r.Context(), run)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "resolve run route evidence failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to resolve route identity")
		return
	}

	items := make([]videoListItem, 0, len(videos))
	ids := make([]string, 0, len(videos))
	for _, v := range videos {
		ids = append(ids, v.VideoID)
	}
	decisionsByVideo := make([][]state.RouteDecision, len(videos))
	var notMatchedIDs []int64
	for i, v := range videos {
		decisions, err := s.runVideoStore.GetRouteDecisionsForVideoInRun(r.Context(), v.ID, runID)
		if err != nil {
			s.logger.ErrorContext(r.Context(), "load route decisions failed", "video_id", v.ID, "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
			writeError(w, http.StatusInternalServerError, "failed to load route decisions")
			return
		}
		s.warnInvalidCausalShapes(r.Context(), runID, decisions)
		decisionsByVideo[i] = decisions
		for _, d := range decisions {
			if d.Decision == state.RouteDecisionNotMatched {
				notMatchedIDs = append(notMatchedIDs, d.ID)
			}
		}
	}
	// One batched read of the condition audits' pass/fail for the whole page.
	failedIndexes, err := s.runVideoStore.ListFailedConditionIndexes(r.Context(), notMatchedIDs)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list failed conditions failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to load route decisions")
		return
	}
	for i, v := range videos {
		decisions := decisionsByVideo[i]
		item, err := toVideoListItem(v, decisions, evidence, run.ConfigVersionID, failedIndexes)
		if err != nil {
			// A decision whose route has no identity mapping fails the
			// whole response; it is never serialized with an integer or
			// a name-only label (ARS-004 failure behavior).
			s.logger.ErrorContext(r.Context(), "serialize route decisions failed", "video_id", v.ID, "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
			writeError(w, http.StatusInternalServerError, "failed to resolve route identity")
			return
		}
		items = append(items, item)
		items[len(items)-1].UnresolvedMutations = projectUnresolvedMutations(mutations[v.ID])
	}
	// Mandatory run and decision evidence is complete before optional metadata.
	local, localErr := s.readLocalMetadata(r.Context(), ids)
	for i := range items {
		items[i].CurrentLocalMetadata, items[i].MetadataContextStatus = local[items[i].VideoID], "available"
		if localErr != nil {
			items[i].MetadataContextStatus = "context_read_failed"
		}
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
	w.Header().Set("Cache-Control", "no-store")
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
		s.logger.ErrorContext(r.Context(), "get run failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to load run")
		return
	}

	video, err := s.runVideoStore.GetVideoByExternalIDInRun(r.Context(), runID, videoID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, "video not found")
			return
		}
		s.logger.ErrorContext(r.Context(), "get run video failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to load video")
		return
	}

	evidence, err := s.runRouteEvidence(r.Context(), run)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "resolve run route evidence failed", "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to resolve route identity")
		return
	}

	detail, err := s.buildVideoDetail(r.Context(), runID, *video, evidence, run.ConfigVersionID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "build video detail failed", "video_id", video.ID, "run_id", runID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
		writeError(w, http.StatusInternalServerError, "failed to build video detail")
		return
	}
	local, localErr := s.readLocalMetadata(r.Context(), []string{video.VideoID})
	detail.CurrentLocalMetadata, detail.MetadataContextStatus = local[video.VideoID], "available"
	if localErr != nil {
		detail.MetadataContextStatus = "context_read_failed"
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, videoDetailResponse{Video: detail})
}

func (s *Server) readLocalMetadata(ctx context.Context, ids []string) (map[string]*metadata.LocalDisplay, error) {
	out := make(map[string]*metadata.LocalDisplay, len(ids))
	if s.metadataReader == nil {
		return out, errors.New("metadata reader not configured")
	}
	values, err := s.metadataReader.ReadLocalDisplay(ctx, ids)
	if err != nil {
		return out, err
	}
	for id, value := range values {
		v := value
		out[id] = &v
	}
	return out, nil
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
	publicIDs    map[int64]string
	labels       map[int64]string
	labelSources map[int64]string
	routes       map[int64]config.SnapshotRoute
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

	routes, err := s.snapshotRoutes(ctx, run)
	if err != nil {
		return nil, err
	}

	// Snapshot labels win; current persisted names fill the gaps.
	labels := make(map[int64]string, len(currentNames))
	sources := make(map[int64]string, len(currentNames))
	for id, name := range currentNames {
		if name != "" {
			labels[id] = name
			sources[id] = "current_local"
		}
	}
	for id, route := range routes {
		if route.Name != "" {
			labels[id] = route.Name
			sources[id] = "run_snapshot"
		}
	}

	return &runRouteEvidence{publicIDs: publicIDs, labels: labels, labelSources: sources, routes: routes}, nil
}

// snapshotRoutes returns unambiguous routes from the run's linked ConfigVersion
// snapshot. It returns a nil map with no error when the run has
// no linked version or the stored snapshot is missing or undecodable: OC-003
// treats that as incomplete configuration context, and the caller falls back
// to current persisted names instead of failing run inspection or fabricating
// labels. A failure to read the version row is a real error, not incomplete
// context.
func (s *Server) snapshotRoutes(ctx context.Context, run *state.Run) (map[int64]config.SnapshotRoute, error) {
	if run.ConfigVersionID == nil {
		return nil, nil
	}
	cv, err := s.configVersionStore.Get(ctx, *run.ConfigVersionID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) || isSnapshotDecodeError(err) {
			s.logger.WarnContext(ctx, "run configuration snapshot unavailable; using current route names",
				"run_id", run.ID, "config_version_id", *run.ConfigVersionID, "diagnostic", diag.Encode(err, "unknown", "inspect_run"))
			return nil, nil
		}
		return nil, fmt.Errorf("load run configuration version %d: %w", *run.ConfigVersionID, err)
	}

	routes := make(map[int64]config.SnapshotRoute, len(cv.Snapshot.Routes))
	duplicates := make(map[int64]bool)
	for _, r := range cv.Snapshot.Routes {
		if _, exists := routes[r.ID]; exists || duplicates[r.ID] {
			delete(routes, r.ID)
			duplicates[r.ID] = true
			continue
		}
		routes[r.ID] = r
	}
	return routes, nil
}

func (e *runRouteEvidence) labelSource(routeID int64) string {
	if source := e.labelSources[routeID]; source != "" {
		return source
	}
	return "unavailable"
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
	evidence *runRouteEvidence, configVersionID *int64) (videoDetail, error) {

	decisions, err := s.runVideoStore.GetRouteDecisionsForVideoInRun(ctx, video.ID, runID)
	if err != nil {
		return videoDetail{}, fmt.Errorf("loading route decisions: %w", err)
	}
	s.warnInvalidCausalShapes(ctx, runID, decisions)
	actions, err := s.runVideoStore.GetExecutedActionsForVideoInRun(ctx, video.ID, runID)
	if err != nil {
		return videoDetail{}, fmt.Errorf("loading executed actions: %w", err)
	}
	mutations, err := s.runVideoStore.ListUnresolvedMutations(ctx, runID, []int64{video.ID})
	if err != nil {
		return videoDetail{}, fmt.Errorf("loading unresolved mutations: %w", err)
	}

	decisionDetails := make([]routeDecisionDetail, 0, len(decisions))
	for _, d := range decisions {
		audits, err := s.runVideoStore.GetConditionAuditsForDecision(ctx, d.ID)
		if err != nil {
			return videoDetail{}, fmt.Errorf("loading condition audits for decision %d: %w", d.ID, err)
		}
		detail, err := toRouteDecisionDetail(d, audits, actions, evidence, configVersionID)
		if err != nil {
			return videoDetail{}, err
		}
		decisionDetails = append(decisionDetails, detail)
	}

	errorMsg, withheld := safeErrorMessage(video.ErrorMessage)
	deferredReason, deferredUnavailable := safeDispositionReason(video.DeferredReason, "video_reason")
	return videoDetail{
		ID:                        video.ID,
		VideoID:                   video.VideoID,
		ChannelID:                 video.ChannelID,
		DiscoveredAt:              video.DiscoveredAt,
		ProcessingState:           string(video.ProcessingState),
		FinalOutcome:              nullableOutcome(video),
		DeferredReason:            deferredReason,
		DeferredReasonUnavailable: deferredUnavailable,
		ErrorMessage:              errorMsg,
		ErrorMessageUnavailable:   withheld,
		CompletedAt:               video.CompletedAt,
		RouteDecisions:            decisionDetails,
		UnresolvedMutations:       projectUnresolvedMutations(mutations[video.ID]),
	}, nil
}

func projectUnresolvedMutations(items []state.UnresolvedMutation) []unresolvedMutationDetail {
	out := make([]unresolvedMutationDetail, 0, len(items))
	for _, item := range items {
		out = append(out, unresolvedMutationDetail{ActionType: router.ToPublicActionType(item.ActionType), TargetPlaylistID: item.TargetPlaylistID, PreparedAt: item.PreparedAt, ApplicationState: "unknown"})
	}
	return out
}

func toVideoListItem(video state.Video, decisions []state.RouteDecision,
	evidence *runRouteEvidence, configVersionID *int64, failedIndexes map[int64][]int) (videoListItem, error) {

	summaries := make([]routeDecisionSummary, 0, len(decisions))
	for _, d := range decisions {
		publicID, err := evidence.publicID(d.RouteID)
		if err != nil {
			return videoListItem{}, err
		}
		summaries = append(summaries, routeDecisionSummary{
			RouteID:         publicID,
			RouteName:       evidence.label(d.RouteID),
			RouteNameSource: evidence.labelSource(d.RouteID),
			EvaluationOrder: d.EvaluationOrder,
			Decision:        string(d.Decision),
			SkipReason:      skipReasonString(d.SkipReason),
			RejectionReason: d.RejectionReason,
			CausalExclusion: projectCausalExclusion(d, configVersionID, evidence),
			FailedCondition: projectFailedCondition(d, evidence, failedIndexes[d.ID]),
		})
	}
	errorMsg, withheld := safeErrorMessage(video.ErrorMessage)
	deferredReason, deferredUnavailable := safeDispositionReason(video.DeferredReason, "video_reason")
	return videoListItem{
		ID:                        video.ID,
		VideoID:                   video.VideoID,
		ChannelID:                 video.ChannelID,
		DiscoveredAt:              video.DiscoveredAt,
		ProcessingState:           string(video.ProcessingState),
		FinalOutcome:              nullableOutcome(video),
		DeferredReason:            deferredReason,
		DeferredReasonUnavailable: deferredUnavailable,
		ErrorMessage:              errorMsg,
		ErrorMessageUnavailable:   withheld,
		CompletedAt:               video.CompletedAt,
		RouteDecisions:            summaries,
	}, nil
}

func toRouteDecisionDetail(d state.RouteDecision, audits []state.ConditionAudit,
	actions []state.ExecutedAction, evidence *runRouteEvidence, configVersionID *int64) (routeDecisionDetail, error) {

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
		actionMsg, withheld := safeErrorMessage(a.ErrorMessage)
		outcomeReason, outcomeUnavailable := safeDispositionReason(a.OutcomeReason, "action_reason")
		actionDetails = append(actionDetails, executedActionDetail{
			ActionType:               router.ToPublicActionType(a.ActionType),
			TargetPlaylistID:         a.TargetPlaylistID,
			Outcome:                  string(a.Outcome),
			OutcomeReason:            outcomeReason,
			OutcomeReasonUnavailable: outcomeUnavailable,
			ErrorMessage:             actionMsg,
			ErrorMessageUnavailable:  withheld,
			ExecutedAt:               a.ExecutedAt,
		})
	}

	return routeDecisionDetail{
		RouteID:         publicID,
		RouteName:       evidence.label(d.RouteID),
		RouteNameSource: evidence.labelSource(d.RouteID),
		EvaluationOrder: d.EvaluationOrder,
		Decision:        string(d.Decision),
		SkipReason:      skipReasonString(d.SkipReason),
		RejectionReason: d.RejectionReason,
		ConditionAudits: auditDetails,
		ExecutedActions: actionDetails,
		CausalExclusion: projectCausalExclusion(d, configVersionID, evidence),
	}, nil
}

func projectCausalExclusion(d state.RouteDecision, configVersionID *int64, evidence *runRouteEvidence) *causalExclusion {
	if d.Decision != state.RouteDecisionSkipped || d.SkipReason == nil || *d.SkipReason != state.SkipReasonExclusion {
		return nil
	}
	out := &causalExclusion{
		Status:                "not_recorded",
		ConfiguredValueStatus: "unavailable", ComparedInputStatus: "unavailable",
	}
	if configVersionID != nil && *configVersionID > 0 {
		out.ConfigVersionID = configVersionID
	}
	if d.ExclusionIndex != nil && *d.ExclusionIndex >= 0 {
		out.ExclusionIndex = d.ExclusionIndex
	}
	if d.ExclusionSemanticsVersion != nil && *d.ExclusionSemanticsVersion > 0 {
		out.SemanticsVersion = d.ExclusionSemanticsVersion
	}
	if d.ExclusionIndex == nil && d.ExclusionSemanticsVersion == nil {
		return out
	}
	if out.ExclusionIndex == nil || out.SemanticsVersion == nil {
		out.Status = "invalid_reference"
		return out
	}
	if *out.SemanticsVersion != 1 {
		out.Status = "unsupported"
		return out
	}
	route, ok := evidence.routes[d.RouteID]
	if !ok || out.ConfigVersionID == nil {
		out.Status = "definition_unavailable"
		return out
	}
	if *out.ExclusionIndex >= len(route.Exclusions) {
		out.Status = "invalid_reference"
		return out
	}
	ex := route.Exclusions[*out.ExclusionIndex]
	if !ex.HasType() {
		out.Status = "definition_unavailable"
		return out
	}
	var field, comparison string
	switch ex.ExclusionType {
	case "channel":
		field, comparison = "channel_id", "exact"
	case "video":
		field, comparison = "video_id", "exact"
	case "title":
		field, comparison = "title", "trimmed_equal_fold"
	case "word":
		field, comparison = "title", "lowercase_substring"
	case "duration_min", "duration_max":
		if ex.HasValue() {
			if _, err := strconv.Atoi(ex.ExclusionValue); err != nil {
				out.Status = "invalid_reference"
				return out
			}
		}
		field = "duration_seconds"
		if ex.ExclusionType == "duration_min" {
			comparison = "strict_less_than"
		} else {
			comparison = "strict_greater_than"
		}
	default:
		out.Status = "unsupported"
		return out
	}
	out.ExclusionType, out.Field, out.Comparison = &ex.ExclusionType, &field, &comparison
	if !ex.HasValue() {
		// Rule identity, type and comparison stay known; only the value is missing.
		out.Status = "definition_unavailable"
		return out
	}
	out.Status = "available"
	matched := true
	out.Matched = &matched
	// The configured value is the user's own configuration (BR-015 AC11): it is
	// released unchanged, in run-video responses only.
	value := ex.ExclusionValue
	out.ConfiguredValue = &value
	out.ConfiguredValueStatus = "available"
	return out
}

// projectFailedCondition builds the list's failed-condition summary. It returns
// nil, never a guess, when the decision did not fail on conditions, the run has
// no usable snapshot for the route, or the snapshot cannot name a known field
// and operator for the first failed condition. The list then reads "no match".
func projectFailedCondition(d state.RouteDecision, evidence *runRouteEvidence, failed []int) *failedCondition {
	if d.Decision != state.RouteDecisionNotMatched || len(failed) == 0 {
		return nil
	}
	route, ok := evidence.routes[d.RouteID]
	if !ok {
		return nil
	}
	var first *config.SnapshotCondition
	for i := range route.Conditions {
		if route.Conditions[i].ConditionIndex == failed[0] {
			first = &route.Conditions[i]
			break
		}
	}
	if first == nil || !first.HasRequiredFields() ||
		!knownConditionField(first.Field) || !knownConditionOperator(first.Operator) {
		return nil
	}
	composition := "AND"
	if route.ConditionComposition == "OR" {
		composition = "OR"
	}
	return &failedCondition{
		Field:          first.Field,
		Operator:       first.Operator,
		Value:          first.Value,
		Composition:    composition,
		ConditionCount: len(route.Conditions),
		FurtherFailed:  len(failed) - 1,
	}
}

func knownConditionField(field string) bool {
	switch router.ConditionField(field) {
	case router.ConditionFieldTitle, router.ConditionFieldChannelID, router.ConditionFieldChannelName,
		router.ConditionFieldDescription, router.ConditionFieldDurationSeconds:
		return true
	}
	return false
}

func knownConditionOperator(operator string) bool {
	switch router.ConditionOperator(operator) {
	case router.ConditionOperatorContains, router.ConditionOperatorEquals, router.ConditionOperatorStartsWith,
		router.ConditionOperatorEndsWith, router.ConditionOperatorMatchesRegex, router.ConditionOperatorGreaterThan,
		router.ConditionOperatorLessThan, router.ConditionOperatorBetween:
		return true
	}
	return false
}

func (s *Server) warnInvalidCausalShapes(ctx context.Context, runID int64, decisions []state.RouteDecision) {
	for _, d := range decisions {
		if (d.ExclusionIndex != nil || d.ExclusionSemanticsVersion != nil) &&
			(d.Decision != state.RouteDecisionSkipped || d.SkipReason == nil || *d.SkipReason != state.SkipReasonExclusion) {
			s.logger.WarnContext(ctx, "non-exclusion decision has causal reference", "run_id", runID, "decision_id", d.ID)
		}
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

// compile-time interface check: config.Store implements ConfigVersionStore.
var _ ConfigVersionStore = (*config.Store)(nil)
