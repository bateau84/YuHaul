package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/pipeline"
	"github.com/bateau84/YuHaul/internal/queue"
	"github.com/bateau84/YuHaul/internal/state"
)

// X1 for the stranded-video routes (run-initiation ARS-005 §8, §11.9 test 39):
// every row of the §8.1 and §8.2 tables through the registered handlers, runWG
// balanced, no raw text in any body.

// strandedFixture is a scan that fills every field of §8.1.
func strandedFixture() pipeline.StrandedReport {
	var r pipeline.StrandedReport
	r.Requeue = 70000
	r.AlreadyQueued.Videos, r.AlreadyQueued.InErrorsQueue = 1180, 2
	r.RemovedByUser = 3
	r.Blocked.Videos = 41
	r.Blocked.ByReason = map[string]int{pipeline.BlockUncertainAddition: 2, pipeline.BlockUnresolvedSendGuard: 4, pipeline.BlockAmbiguousLegacyEvidence: 35}
	r.Blocked.Items = []pipeline.BlockedVideo{
		{VideoID: 812, YouTubeVideoID: "dQw4w9WgXcQ", Reasons: []pipeline.BlockReason{{Reason: pipeline.BlockAmbiguousLegacyEvidence, RunID: 14}}},
		{VideoID: 900, YouTubeVideoID: "abcdefghijk", Reasons: []pipeline.BlockReason{
			{Reason: pipeline.BlockUncertainAddition, RunID: 51}, {Reason: pipeline.BlockUnresolvedSendGuard, RunID: 52}}},
	}
	r.NotEnded.Videos = 12
	r.NotEnded.Runs = []pipeline.RunVideos{{RunID: 52, Videos: 12}}
	r.NoOriginRun = 1
	r.RetainedWithoutQueueItem.Videos, r.RetainedWithoutQueueItem.RemovedByUser = 150, 40
	return r
}

// strandedReportJSON is strandedFixture as §8.1 shows it.
const strandedReportJSON = `{
  "requeue": 70000,
  "already_queued": {"videos": 1180, "in_errors_queue": 2},
  "removed_by_user": 3,
  "blocked": {
    "videos": 41,
    "by_reason": {"uncertain_addition": 2, "unresolved_send_guard": 4, "ambiguous_legacy_evidence": 35},
    "items": [
      {"video_id": 812, "youtube_video_id": "dQw4w9WgXcQ", "reasons": [{"reason": "ambiguous_legacy_evidence", "run_id": 14}]},
      {"video_id": 900, "youtube_video_id": "abcdefghijk", "reasons": [{"reason": "uncertain_addition", "run_id": 51}, {"reason": "unresolved_send_guard", "run_id": 52}]}
    ]
  },
  "not_ended": {"videos": 12, "runs": [{"run_id": 52, "videos": 12}]},
  "no_origin_run": 1,
  "retained_without_queue_item": {"videos": 150, "removed_by_user": 40}
}`

func do(srv *Server, method, target, body string) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, target, rd))
	return rec
}

func requireJSONEq(t *testing.T, want string, rec *httptest.ResponseRecorder) {
	t.Helper()
	require.JSONEq(t, want, rec.Body.String())
}

// merge returns the §8.1 report with extra members added.
func merge(t *testing.T, extra string) string {
	t.Helper()
	var base, add map[string]any
	require.NoError(t, json.Unmarshal([]byte(strandedReportJSON), &base))
	require.NoError(t, json.Unmarshal([]byte(extra), &add))
	for k, v := range add {
		base[k] = v
	}
	out, err := json.Marshal(base)
	require.NoError(t, err)
	return string(out)
}

func TestStrandedPreviewHTTPContract(t *testing.T) {
	f := &fakePipeline{}
	srv := controlServer(t, f)

	f.previewRes = &pipeline.StrandedPreview{StrandedReport: strandedFixture(), Available: true}
	rec := do(srv, http.MethodGet, "/api/stranded-videos", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	requireJSONEq(t, merge(t, `{"available":true,"unavailable_reason":null}`), rec)

	// A refusal reason arrives with the counts that are still current.
	for _, reason := range []string{pipeline.RequeueUnavailableStartupRecovery, pipeline.RequeueUnavailableProductionRun} {
		f.previewRes = &pipeline.StrandedPreview{StrandedReport: strandedFixture(), UnavailableReason: reason}
		rec = do(srv, http.MethodGet, "/api/stranded-videos", "")
		require.Equal(t, http.StatusOK, rec.Code)
		requireJSONEq(t, merge(t, fmt.Sprintf(`{"available":false,"unavailable_reason":%q}`, reason)), rec)
	}

	// An empty scan keeps every list and map present, never null.
	f.previewRes = &pipeline.StrandedPreview{Available: true}
	rec = do(srv, http.MethodGet, "/api/stranded-videos", "")
	require.Equal(t, http.StatusOK, rec.Code)
	requireJSONEq(t, `{"available":true,"unavailable_reason":null,"requeue":0,
		"already_queued":{"videos":0,"in_errors_queue":0},"removed_by_user":0,
		"blocked":{"videos":0,"by_reason":{},"items":[]},
		"not_ended":{"videos":0,"runs":[]},"no_origin_run":0,
		"retained_without_queue_item":{"videos":0,"removed_by_user":0}}`, rec)

	// A failed read answers the closed body and none of its cause.
	f.previewRes, f.previewErr = nil, errors.New("sqlite: disk I/O error at /var/lib/yuhaul/yuhaul.db")
	rec = do(srv, http.MethodGet, "/api/stranded-videos", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	requireJSONEq(t, `{"error":"the stranded videos could not be counted","code":"preview_failed"}`, rec)
	require.NotContains(t, rec.Body.String(), "sqlite")
	require.NotContains(t, rec.Body.String(), "yuhaul.db")
}

// Once shutdown has begun the handler, not the pipeline, says so, and it
// overrides the pipeline's own reason and availability (ARS-005 §4).
func TestStrandedPreviewReportsShutdown(t *testing.T) {
	for name, begin := range map[string]func(*Server, context.CancelFunc){
		"WaitRuns":          func(s *Server, _ context.CancelFunc) { require.NoError(t, s.WaitRuns(context.Background())) },
		"run context ended": func(_ *Server, cancel context.CancelFunc) { cancel() },
	} {
		t.Run(name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakePipeline{previewRes: &pipeline.StrandedPreview{
				StrandedReport: strandedFixture(), Available: true,
			}}
			srv := controlServer(t, f, WithRunContext(base))
			begin(srv, cancel)
			rec := do(srv, http.MethodGet, "/api/stranded-videos", "")
			require.Equal(t, http.StatusOK, rec.Code)
			requireJSONEq(t, merge(t, `{"available":false,"unavailable_reason":"shutting_down"}`), rec)

			f.previewRes = &pipeline.StrandedPreview{StrandedReport: strandedFixture(), UnavailableReason: pipeline.RequeueUnavailableProductionRun}
			rec = do(srv, http.MethodGet, "/api/stranded-videos", "")
			requireJSONEq(t, merge(t, `{"available":false,"unavailable_reason":"shutting_down"}`), rec)
		})
	}
}

func TestRequeueStrandedHTTPContract(t *testing.T) {
	f := &fakePipeline{}
	srv := controlServer(t, f)
	post := func(body string) *httptest.ResponseRecorder {
		return do(srv, http.MethodPost, "/api/stranded-videos/requeue", body)
	}
	intp := func(n int) *int { return &n }

	// Inserted, and the count matches the preview.
	f.requeueRes = &pipeline.RequeueResult{Report: strandedFixture(), Requeued: 70000, Expected: intp(70000)}
	rec := post(`{"expected_requeue":70000}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	requireJSONEq(t, merge(t, `{"outcome":"requeued","requeued":70000,"skipped_concurrently":0,"expected_requeue":70000,"differs_from_preview":false}`), rec)
	require.Equal(t, 70000, *f.requeueExpected)

	// Fewer were written than planned: the difference is reported.
	f.requeueRes = &pipeline.RequeueResult{Report: strandedFixture(), Requeued: 69998, SkippedConcurrently: 2, Expected: intp(70000)}
	rec = post(`{"expected_requeue":70000}`)
	requireJSONEq(t, merge(t, `{"outcome":"requeued","requeued":69998,"skipped_concurrently":2,"expected_requeue":70000,"differs_from_preview":true}`), rec)

	// No expectation: nothing to differ from, and null is echoed.
	for _, body := range []string{``, `{}`, `{"expected_requeue":null}`, " \n"} {
		f.requeueExpected = intp(-1)
		f.requeueRes = &pipeline.RequeueResult{Report: strandedFixture(), Requeued: 5}
		rec = post(body)
		require.Equal(t, http.StatusOK, rec.Code, "body %q", body)
		requireJSONEq(t, merge(t, `{"outcome":"requeued","requeued":5,"skipped_concurrently":0,"expected_requeue":null,"differs_from_preview":false}`), rec)
		require.Nil(t, f.requeueExpected, "body %q", body)
	}

	// Nothing to do, with and without a count the user saw.
	f.requeueRes = &pipeline.RequeueResult{Report: pipeline.StrandedReport{}, Expected: intp(0)}
	rec = post(`{"expected_requeue":0}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"outcome":"nothing_to_requeue"`)
	require.Contains(t, rec.Body.String(), `"requeued":0`)
	require.Contains(t, rec.Body.String(), `"differs_from_preview":false`)
	f.requeueRes = &pipeline.RequeueResult{Report: pipeline.StrandedReport{}, Expected: intp(12)}
	rec = post(`{"expected_requeue":12}`)
	require.Contains(t, rec.Body.String(), `"outcome":"nothing_to_requeue"`)
	require.Contains(t, rec.Body.String(), `"differs_from_preview":true`)

	require.NoError(t, srv.WaitRuns(shortCtx(t)), "success paths must leave runWG balanced")
}

func TestRequeueStrandedRefusals(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"startup recovery", pipeline.ErrStartupRecoveryActive, 503,
			fmt.Sprintf(`{"error":%q,"code":"startup_recovery_active"}`, pipeline.ErrStartupRecoveryActive.Error())},
		{"wrapped startup recovery", fmt.Errorf("x: %w", pipeline.ErrStartupRecoveryActive), 503,
			fmt.Sprintf(`{"error":%q,"code":"startup_recovery_active"}`, pipeline.ErrStartupRecoveryActive.Error())},
		{"production run active", pipeline.ErrProductionRunActive, 409,
			`{"error":"a production run is already active","code":"production_run_active"}`},
		{"not applied", pipeline.ErrRequeueNotApplied, 500,
			`{"error":"the re-queue did not take effect; nothing changed","code":"requeue_failed"}`},
		{"outcome unknown", pipeline.ErrRequeueOutcomeUnknown, 500,
			`{"error":"could not confirm whether the videos were re-queued; reload the preview; repeating is safe","code":"requeue_outcome_unknown"}`},
		// Anything the pipeline does not classify is "not applied", with no cause shown.
		{"unclassified", errors.New("UNIQUE constraint failed: deferred_item.video_id; token=secret"), 500,
			`{"error":"the re-queue did not take effect; nothing changed","code":"requeue_failed"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakePipeline{requeueErr: tc.err}
			srv := controlServer(t, f)
			rec := do(srv, http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":7}`)
			require.Equal(t, tc.status, rec.Code)
			requireJSONEq(t, tc.body, rec)
			require.NotContains(t, rec.Body.String(), "secret")
			require.NotContains(t, rec.Body.String(), "UNIQUE")
			require.NoError(t, srv.WaitRuns(shortCtx(t)), "a refusal must leave runWG balanced")
		})
	}
}

func TestRequeueStrandedRejectsABadBodyBeforeAnythingElse(t *testing.T) {
	bad := []string{
		`{"expected_requeue":-1}`,
		`{"expected_requeue":"7"}`,
		`{"expected_requeue":1.5}`,
		`{"expected_requeue":true}`,
		`{"expected_requeue":99999999999999999999}`,
		`{"expected_requeue":1,"other":2}`,
		`{"expected":1}`,
		`{"expected_requeue":1}{"expected_requeue":2}`,
		`{"expected_requeue":1} x`,
		`[]`, `7`, `"x"`, `{`, `not json`,
	}
	for _, body := range bad {
		f := &fakePipeline{requeueRes: &pipeline.RequeueResult{}}
		srv := controlServer(t, f)
		rec := do(srv, http.MethodPost, "/api/stranded-videos/requeue", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, "body %q", body)
		requireJSONEq(t, `{"error":"invalid request body","code":"invalid_body"}`, rec)
		require.Zero(t, f.requeueCalls, "body %q must not reach the pipeline", body)
		require.NoError(t, srv.WaitRuns(shortCtx(t)), "a bad body must not register a run")
	}
}

// After shutdown begins, a confirm is refused before it reaches the pipeline.
func TestRequeueStrandedRefusedAfterShutdownBegins(t *testing.T) {
	for name, begin := range map[string]func(*Server, context.CancelFunc){
		"WaitRuns":          func(s *Server, _ context.CancelFunc) { require.NoError(t, s.WaitRuns(context.Background())) },
		"run context ended": func(_ *Server, cancel context.CancelFunc) { cancel() },
	} {
		t.Run(name, func(t *testing.T) {
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakePipeline{requeueRes: &pipeline.RequeueResult{Requeued: 1}}
			srv := controlServer(t, f, WithRunContext(base))
			begin(srv, cancel)
			rec := do(srv, http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":1}`)
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			requireJSONEq(t, `{"error":"server is shutting down","code":"shutting_down"}`, rec)
			require.Zero(t, f.requeueCalls, "a refused confirm must not reach the pipeline")

			// A malformed body is still a 400: the body is parsed first (§8.2 step 1).
			rec = do(srv, http.MethodPost, "/api/stranded-videos/requeue", `{"expected_requeue":-1}`)
			require.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// A confirm that is already writing when shutdown begins finishes: the drain
// waits for it, its context is neither the request's nor the run context, and
// its answer is the real one (BR-037 AC9, ARS-005 §8.2).
func TestRequeueStrandedInFlightJoinsTheShutdownDrain(t *testing.T) {
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	f := &fakePipeline{
		requeueRes:     &pipeline.RequeueResult{Report: strandedFixture(), Requeued: 3, Expected: ptr(3)},
		requeueEntered: make(chan context.Context),
		requeueRelease: make(chan struct{}),
	}
	srv := controlServer(t, f, WithRunContext(base))

	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()
	req := httptest.NewRequest(http.MethodPost, "/api/stranded-videos/requeue", strings.NewReader(`{"expected_requeue":3}`)).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() { srv.Handler().ServeHTTP(rec, req); close(served) }()

	var pctx context.Context
	select {
	case pctx = <-f.requeueEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler never reached the pipeline")
	}
	dl, ok := pctx.Deadline()
	require.True(t, ok, "the confirm context must be bounded")
	require.WithinDuration(t, time.Now().Add(requeueDeadline), dl, 15*time.Second)

	// The client disconnects and the process starts to shut down.
	cancelReq()
	cancelBase()
	require.NoError(t, pctx.Err(), "neither a disconnect nor shutdown may cancel the write")

	drained := make(chan error, 1)
	go func() { drained <- srv.WaitRuns(context.Background()) }()
	select {
	case err := <-drained:
		t.Fatalf("the drain returned while the confirm was writing: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	require.ErrorIs(t, srv.WaitRuns(shortCtx(t)), context.DeadlineExceeded)

	close(f.requeueRelease)
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("the handler did not return")
	}
	require.NoError(t, <-drained)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"outcome":"requeued"`)
}

func ptr[T any](v T) *T { return &v }

func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}

// X3 (ARS-005 §0, §11.9 test 41): in the composed server, where
// /api/stranded-videos is registered, GET /api/deferred without parameters
// returns one bounded page and carries the total. The server is composed with
// the real queue service over a migrated database holding more items than a
// page.
func TestStrandedRoutesAreRegisteredOnlyWithThePagedDeferredQueue(t *testing.T) {
	ctx := context.Background()
	db, stateStore, _, cleanup := setupServerTest(t)
	defer cleanup()
	qs, err := queue.NewStore(db)
	require.NoError(t, err)

	runID, err := stateStore.CreateRun(ctx, &state.Run{StartedAt: time.Now().UTC(), RunType: state.RunTypeManual, Status: state.RunStatusFailed})
	require.NoError(t, err)
	const items = 260
	for i := 0; i < items; i++ {
		vid, err := stateStore.CreateVideo(ctx, &state.Video{
			VideoID: fmt.Sprintf("x3video%04d", i), ChannelID: "c1", DiscoveredAt: time.Now().UTC(),
			DiscoveredInRunID: runID, ProcessingState: state.VideoStateDiscovered, FinalOutcome: state.FinalOutcomeNotYetEvaluated,
		})
		require.NoError(t, err)
		_, err = qs.CreateDeferredItem(ctx, &queue.DeferredItem{
			VideoID: vid, RunID: runID, Reason: queue.DeferredReasonRequeuedByUser,
			Status: queue.DeferredStatusPending, CreatedAt: time.Now().UTC(),
		})
		require.NoError(t, err)
	}

	f := &fakePipeline{previewRes: &pipeline.StrandedPreview{Available: true}}
	srv := NewServer(f, pipeline.NewBroadcaster(),
		WithServerLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
		WithQueueHandler(queue.NewService(qs, nil)))

	rec := do(srv, http.MethodGet, "/api/stranded-videos", "")
	require.Equal(t, http.StatusOK, rec.Code, "the stranded route must be registered here")
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	rec = do(srv, http.MethodGet, "/api/deferred", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var page struct {
		Deferred []json.RawMessage `json:"deferred"`
		Total    *int              `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &page))
	require.NotNil(t, page.Total, "the deferred read must carry total")
	require.Equal(t, items, *page.Total)
	require.NotEmpty(t, page.Deferred)
	require.LessOrEqual(t, len(page.Deferred), queue.MaxDeferredPageLimit)
	require.Less(t, len(page.Deferred), items, "one bounded page, not the queue")
}
