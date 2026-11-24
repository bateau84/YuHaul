package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
	"github.com/bateau84/YuHaul/internal/pipeline"
)

// The two stranded-video routes (run-initiation ARS-005 §8, BR-037). They are
// registered next to POST /api/runs and may only be registered together with
// the paged GET /api/deferred (ARS-005 §0; test X3).

// requeueDeadline bounds one confirmation. The confirmation runs on a context
// that is detached from the request, so a client that disconnects, or a
// shutdown that cancels the run context, cannot abort the single write
// transaction half way (BR-037 AC9).
const requeueDeadline = 2 * time.Minute

// maxRequeueBody bounds the confirm body, which is one small object.
const maxRequeueBody = 1 << 12

// strandedReportBody is the wire shape of a scan (ARS-005 §8.1) without the
// availability fields. Both routes embed it.
type strandedReportBody struct {
	Requeue       int `json:"requeue"`
	AlreadyQueued struct {
		Videos        int `json:"videos"`
		InErrorsQueue int `json:"in_errors_queue"`
	} `json:"already_queued"`
	RemovedByUser int `json:"removed_by_user"`
	Blocked       struct {
		Videos   int                `json:"videos"`
		ByReason map[string]int     `json:"by_reason"`
		Items    []blockedVideoBody `json:"items"`
	} `json:"blocked"`
	NotEnded struct {
		Videos int               `json:"videos"`
		Runs   []notEndedRunBody `json:"runs"`
	} `json:"not_ended"`
	NoOriginRun              int `json:"no_origin_run"`
	RetainedWithoutQueueItem struct {
		Videos        int `json:"videos"`
		RemovedByUser int `json:"removed_by_user"`
	} `json:"retained_without_queue_item"`
}

type blockedVideoBody struct {
	VideoID        int64             `json:"video_id"`
	YouTubeVideoID string            `json:"youtube_video_id"`
	Reasons        []blockReasonBody `json:"reasons"`
}

type blockReasonBody struct {
	Reason string `json:"reason"`
	RunID  int64  `json:"run_id"`
}

type notEndedRunBody struct {
	RunID  int64 `json:"run_id"`
	Videos int   `json:"videos"`
}

// newStrandedReportBody copies a scan into its wire shape. Every collection
// is non-nil, so the SPA never reads null for an empty list or map.
func newStrandedReportBody(r pipeline.StrandedReport) strandedReportBody {
	var b strandedReportBody
	b.Requeue = r.Requeue
	b.AlreadyQueued.Videos = r.AlreadyQueued.Videos
	b.AlreadyQueued.InErrorsQueue = r.AlreadyQueued.InErrorsQueue
	b.RemovedByUser = r.RemovedByUser
	b.Blocked.Videos = r.Blocked.Videos
	b.Blocked.ByReason = make(map[string]int, len(r.Blocked.ByReason))
	for reason, n := range r.Blocked.ByReason {
		b.Blocked.ByReason[reason] = n
	}
	b.Blocked.Items = make([]blockedVideoBody, 0, len(r.Blocked.Items))
	for _, item := range r.Blocked.Items {
		reasons := make([]blockReasonBody, 0, len(item.Reasons))
		for _, reason := range item.Reasons {
			reasons = append(reasons, blockReasonBody{Reason: reason.Reason, RunID: reason.RunID})
		}
		b.Blocked.Items = append(b.Blocked.Items, blockedVideoBody{VideoID: item.VideoID, YouTubeVideoID: item.YouTubeVideoID, Reasons: reasons})
	}
	b.NotEnded.Videos = r.NotEnded.Videos
	b.NotEnded.Runs = make([]notEndedRunBody, 0, len(r.NotEnded.Runs))
	for _, run := range r.NotEnded.Runs {
		b.NotEnded.Runs = append(b.NotEnded.Runs, notEndedRunBody{RunID: run.RunID, Videos: run.Videos})
	}
	b.NoOriginRun = r.NoOriginRun
	b.RetainedWithoutQueueItem.Videos = r.RetainedWithoutQueueItem.Videos
	b.RetainedWithoutQueueItem.RemovedByUser = r.RetainedWithoutQueueItem.RemovedByUser
	return b
}

type strandedPreviewBody struct {
	Available         bool    `json:"available"`
	UnavailableReason *string `json:"unavailable_reason"`
	strandedReportBody
}

type requeueResultBody struct {
	Outcome             string `json:"outcome"`
	Requeued            int    `json:"requeued"`
	SkippedConcurrently int    `json:"skipped_concurrently"`
	ExpectedRequeue     *int   `json:"expected_requeue"`
	DiffersFromPreview  bool   `json:"differs_from_preview"`
	strandedReportBody
}

// handleStrandedPreview is GET /api/stranded-videos (ARS-005 §8.1). It takes
// no lock, changes nothing and makes no YouTube call. The pipeline does not
// know about shutdown, so once shutdown has begun the handler reports it and
// overrides any pipeline reason (ARS-005 §4).
func (s *Server) handleStrandedPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	preview, err := s.pipeline.PreviewStranded(r.Context())
	if err != nil {
		if s.clientAborted(r.Context(), "stranded videos preview failed", err) {
			return
		}
		s.logger.ErrorContext(r.Context(), "stranded videos preview failed", "diagnostic", diag.Encode(err, "unknown", "requeue_stranded"))
		writeErrorCode(w, http.StatusInternalServerError, "preview_failed", "the stranded videos could not be counted", nil)
		return
	}
	body := strandedPreviewBody{Available: preview.Available, strandedReportBody: newStrandedReportBody(preview.StrandedReport)}
	reason := preview.UnavailableReason
	if s.shuttingDown() {
		reason = pipeline.RequeueUnavailableShuttingDown
		body.Available = false
	}
	if reason != "" {
		body.UnavailableReason = &reason
	}
	writeJSON(w, http.StatusOK, body)
}

// requeueRequest is the confirm body: the count the user saw in the preview,
// or nothing. ExpectedRequeue is only reported back; it never limits the write.
type requeueRequest struct {
	ExpectedRequeue *int `json:"expected_requeue"`
}

// parseRequeueRequest reads the body: empty, or one object whose only member
// is expected_requeue, a non-negative integer or null.
func parseRequeueRequest(r *http.Request) (requeueRequest, bool) {
	var req requeueRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxRequeueBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return requeueRequest{}, errors.Is(err, io.EOF)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return requeueRequest{}, false
	}
	if req.ExpectedRequeue != nil && *req.ExpectedRequeue < 0 {
		return requeueRequest{}, false
	}
	return req, true
}

// handleRequeueStranded is POST /api/stranded-videos/requeue (ARS-005 §8.2).
// Handler order: parse, trackRun, RequeueStranded on a detached bounded
// context, runWG.Done, respond. The confirmation therefore joins the
// shutdown drain like a run does: once shutdown has begun it is refused, and
// one already writing finishes before WaitRuns returns.
func (s *Server) handleRequeueStranded(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	req, ok := parseRequeueRequest(r)
	if !ok {
		writeErrorCode(w, http.StatusBadRequest, "invalid_body", "invalid request body", nil)
		return
	}
	if !s.trackRun() {
		writeErrorCode(w, http.StatusServiceUnavailable, "shutting_down", "server is shutting down", nil)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), requeueDeadline)
	res, err := s.pipeline.RequeueStranded(ctx, req.ExpectedRequeue)
	cancel()
	s.runWG.Done()
	if err != nil {
		s.writeRequeueRefusal(w, r, err)
		return
	}
	outcome := "requeued"
	if res.Requeued == 0 {
		outcome = "nothing_to_requeue"
	}
	writeJSON(w, http.StatusOK, requeueResultBody{
		Outcome:             outcome,
		Requeued:            res.Requeued,
		SkippedConcurrently: res.SkippedConcurrently,
		ExpectedRequeue:     res.Expected,
		DiffersFromPreview:  res.Expected != nil && *res.Expected != res.Requeued,
		strandedReportBody:  newStrandedReportBody(res.Report),
	})
}

// writeRequeueRefusal maps the closed set of failures to their status and
// code. Bodies carry no raw error, database, credential or provider text.
func (s *Server) writeRequeueRefusal(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, pipeline.ErrStartupRecoveryActive):
		writeErrorCode(w, http.StatusServiceUnavailable, "startup_recovery_active", pipeline.ErrStartupRecoveryActive.Error(), nil)
	case errors.Is(err, pipeline.ErrProductionRunActive):
		writeErrorCode(w, http.StatusConflict, "production_run_active", pipeline.ErrProductionRunActive.Error(), nil)
	case errors.Is(err, pipeline.ErrRequeueOutcomeUnknown):
		writeErrorCode(w, http.StatusInternalServerError, "requeue_outcome_unknown", "could not confirm whether the videos were re-queued; reload the preview; repeating is safe", nil)
	default:
		if !errors.Is(err, pipeline.ErrRequeueNotApplied) {
			s.logger.ErrorContext(r.Context(), "stranded videos requeue failed", "diagnostic", diag.Encode(err, "unknown", "requeue_stranded"))
		}
		writeErrorCode(w, http.StatusInternalServerError, "requeue_failed", "the re-queue did not take effect; nothing changed", nil)
	}
}
