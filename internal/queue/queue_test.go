package queue

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/store/storetest"
)

func TestAuthorizationPendingDeferredReasonIsValid(t *testing.T) {
	if !DeferredReasonAuthorizationPending.Valid() {
		t.Fatal("authorization_pending must be accepted by queue writers and readers")
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return storetest.Open(t)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("creating queue store: %v", err)
	}
	return store
}

func seedRun(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO run (started_at, run_type, status) VALUES (?, ?, ?);`,
		time.Now().UTC(), "manual", "running")
	if err != nil {
		t.Fatalf("creating run: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading run id: %v", err)
	}
	return id
}

func seedVideo(t *testing.T, db *sql.DB, runID int64, externalID string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO video (video_id, channel_id, discovered_in_run_id, processing_state, final_outcome)
		 VALUES (?, ?, ?, ?, ?);`,
		externalID, "UCchannel", runID, "not_yet_evaluated", "not_yet_evaluated")
	if err != nil {
		t.Fatalf("creating video: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading video id: %v", err)
	}
	return id
}

func TestCreateAndListErrorEntries(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-error-1")

	id, err := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID:    vidID,
		RunID:      runID,
		Category:   ErrorCategoryAPIError,
		Message:    "TOKEN_SENTINEL_plain_ascii_7341",
		Detail:     "VIDEO_SENTINEL_plain_ascii_8129",
		Status:     ErrorStatusActive,
		CreatedAt:  time.Now().UTC(),
		RetryCount: 0,
	})
	if err != nil {
		t.Fatalf("CreateErrorEntry failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero error entry id")
	}

	active, err := store.ListErrorEntries(context.Background(), ErrorStatusActive)
	if err != nil {
		t.Fatalf("ListErrorEntries failed: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("got %d active entries, want 1", len(active))
	}
	if active[0].ExternalVideoID != "yt-error-1" {
		t.Errorf("external video id = %q, want yt-error-1", active[0].ExternalVideoID)
	}
	if active[0].Message != "diag:v1 phase=unknown operation=unknown code=unknown_error" || active[0].Detail != active[0].Message {
		t.Errorf("unsafe message/detail were not replaced: message=%q detail=%q", active[0].Message, active[0].Detail)
	}
}

func TestDismissErrorPreservesHistory(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-error-2")

	id, err := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID:   vidID,
		RunID:     runID,
		Category:  ErrorCategoryRuntime,
		Message:   "panic recovered",
		Status:    ErrorStatusActive,
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateErrorEntry failed: %v", err)
	}

	if err := store.DismissErrorEntry(context.Background(), id); err != nil {
		t.Fatalf("DismissErrorEntry failed: %v", err)
	}

	entry, err := store.GetErrorEntry(context.Background(), id)
	if err != nil {
		t.Fatalf("GetErrorEntry failed: %v", err)
	}
	if entry.Status != ErrorStatusDismissed {
		t.Errorf("status = %q, want dismissed", entry.Status)
	}
	if entry.ResolvedAt == nil {
		t.Error("expected ResolvedAt to be set after dismiss")
	}

	// Historical inspection still sees the row.
	all, err := store.ListErrorEntries(context.Background(), "")
	if err != nil {
		t.Fatalf("ListErrorEntries failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d historical entries, want 1", len(all))
	}

	// Active queue no longer shows it.
	active, err := store.ListErrorEntries(context.Background(), ErrorStatusActive)
	if err != nil {
		t.Fatalf("ListErrorEntries active failed: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("got %d active entries after dismiss, want 0", len(active))
	}
}

func TestErrorRetryCounterIncrements(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-error-3")

	id, _ := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID:   vidID,
		RunID:     runID,
		Category:  ErrorCategoryAuthorization,
		Message:   "unauthorized",
		Status:    ErrorStatusActive,
		CreatedAt: time.Now().UTC(),
	})

	if err := store.SetErrorEntryRetrying(context.Background(), id); err != nil {
		t.Fatalf("SetErrorEntryRetrying failed: %v", err)
	}

	entry, err := store.GetErrorEntry(context.Background(), id)
	if err != nil {
		t.Fatalf("GetErrorEntry failed: %v", err)
	}
	if entry.Status != ErrorStatusRetrying {
		t.Errorf("status = %q, want retrying", entry.Status)
	}
	if entry.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", entry.RetryCount)
	}
}

func TestCreateAndListDeferredItems(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-defer-1")

	retryAfter := time.Now().UTC().Add(time.Hour)
	id, err := store.CreateDeferredItem(context.Background(), &DeferredItem{
		VideoID:    vidID,
		RunID:      runID,
		Reason:     DeferredReasonQuotaExhausted,
		RetryAfter: &retryAfter,
		Status:     DeferredStatusPending,
		CreatedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateDeferredItem failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero deferred item id")
	}

	pending, err := store.ListDeferredItems(context.Background(), DeferredStatusPending)
	if err != nil {
		t.Fatalf("ListDeferredItems failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("got %d pending items, want 1", len(pending))
	}
	if pending[0].ExternalVideoID != "yt-defer-1" {
		t.Errorf("external video id = %q, want yt-defer-1", pending[0].ExternalVideoID)
	}
	if pending[0].Reason != DeferredReasonQuotaExhausted {
		t.Errorf("reason = %q, want quota_exhausted", pending[0].Reason)
	}
}

func TestDiscardDeferredPreservesHistory(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-defer-2")

	id, _ := store.CreateDeferredItem(context.Background(), &DeferredItem{
		VideoID:   vidID,
		RunID:     runID,
		Reason:    DeferredReasonAPIRateLimited,
		Status:    DeferredStatusPending,
		CreatedAt: time.Now().UTC(),
	})

	if err := store.DiscardDeferredItem(context.Background(), id); err != nil {
		t.Fatalf("DiscardDeferredItem failed: %v", err)
	}

	item, err := store.GetDeferredItem(context.Background(), id)
	if err != nil {
		t.Fatalf("GetDeferredItem failed: %v", err)
	}
	if item.Status != DeferredStatusDiscarded {
		t.Errorf("status = %q, want discarded", item.Status)
	}

	all, err := store.ListDeferredItems(context.Background(), "")
	if err != nil {
		t.Fatalf("ListDeferredItems failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d historical items, want 1", len(all))
	}

	pending, err := store.ListDeferredItems(context.Background(), DeferredStatusPending)
	if err != nil {
		t.Fatalf("ListDeferredItems pending failed: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("got %d pending items after discard, want 0", len(pending))
	}
}

type mockRetryHandler struct {
	errors map[int64]error
	called []string
}

func (m *mockRetryHandler) HandleErrorRetry(ctx context.Context, videoID int64, externalVideoID string) error {
	m.called = append(m.called, "error:"+externalVideoID)
	if err, ok := m.errors[videoID]; ok {
		return err
	}
	return nil
}

func (m *mockRetryHandler) HandleDeferredRetry(ctx context.Context, videoID int64, externalVideoID string) error {
	m.called = append(m.called, "deferred:"+externalVideoID)
	if err, ok := m.errors[videoID]; ok {
		return err
	}
	return nil
}

func TestServiceRetryErrorResolvesOnSuccess(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-error-4")

	id, _ := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID:   vidID,
		RunID:     runID,
		Category:  ErrorCategoryAPIError,
		Message:   "api failure",
		Status:    ErrorStatusActive,
		CreatedAt: time.Now().UTC(),
	})

	handler := &mockRetryHandler{errors: map[int64]error{}}
	svc := NewService(store, handler)

	entry, err := svc.RetryError(context.Background(), id)
	if err != nil {
		t.Fatalf("RetryError failed: %v", err)
	}
	if entry.Status != ErrorStatusResolved {
		t.Errorf("status = %q, want resolved", entry.Status)
	}
	if len(handler.called) != 1 || handler.called[0] != "error:yt-error-4" {
		t.Errorf("handler called = %v, want [error:yt-error-4]", handler.called)
	}
}

func TestServiceRetryErrorReRecordsOnFailure(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-error-5")

	id, _ := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID:   vidID,
		RunID:     runID,
		Category:  ErrorCategoryAPIError,
		Message:   "api failure",
		Status:    ErrorStatusActive,
		CreatedAt: time.Now().UTC(),
	})

	handler := &mockRetryHandler{errors: map[int64]error{vidID: errors.New("TOKEN_SENTINEL_plain_ascii_7341")}}
	var logOutput bytes.Buffer
	svc := NewService(store, handler).WithLogger(slog.New(slog.NewTextHandler(&logOutput, nil)))

	entry, err := svc.RetryError(context.Background(), id)
	if err != nil {
		t.Fatalf("RetryError failed: %v", err)
	}
	if entry.Status != ErrorStatusActive {
		t.Errorf("status = %q, want active", entry.Status)
	}
	if entry.Message != "diag:v1 phase=unknown operation=retry code=unknown_error" {
		t.Errorf("message = %q, want generic safe failure summary", entry.Message)
	}
	if bytes.Contains(logOutput.Bytes(), []byte("TOKEN_SENTINEL_plain_ascii_7341")) {
		t.Fatalf("marker-free error leaked to queue logs: %s", logOutput.String())
	}
	if entry.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", entry.RetryCount)
	}
}

func TestServiceRetryDeferredCompletesOnSuccess(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-defer-3")

	id, _ := store.CreateDeferredItem(context.Background(), &DeferredItem{
		VideoID:   vidID,
		RunID:     runID,
		Reason:    DeferredReasonQuotaExhausted,
		Status:    DeferredStatusPending,
		CreatedAt: time.Now().UTC(),
	})

	handler := &mockRetryHandler{errors: map[int64]error{}}
	svc := NewService(store, handler)

	item, err := svc.RetryDeferred(context.Background(), id)
	if err != nil {
		t.Fatalf("RetryDeferred failed: %v", err)
	}
	if item.Status != DeferredStatusCompleted {
		t.Errorf("status = %q, want completed", item.Status)
	}
}

func TestServiceRetryDeferredReturnsToPendingOnFailure(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-defer-4")

	id, _ := store.CreateDeferredItem(context.Background(), &DeferredItem{
		VideoID:   vidID,
		RunID:     runID,
		Reason:    DeferredReasonAPIRateLimited,
		Status:    DeferredStatusPending,
		CreatedAt: time.Now().UTC(),
	})

	handler := &mockRetryHandler{errors: map[int64]error{vidID: errors.New("rate limited")}}
	svc := NewService(store, handler)

	item, err := svc.RetryDeferred(context.Background(), id)
	if err != nil {
		t.Fatalf("RetryDeferred failed: %v", err)
	}
	if item.Status != DeferredStatusPending {
		t.Errorf("status = %q, want pending", item.Status)
	}
	if item.RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", item.RetryCount)
	}
}

func TestErrorHandlerListAndDismiss(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-error-http")

	id, _ := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID:   vidID,
		RunID:     runID,
		Category:  ErrorCategoryAPIError,
		Message:   "api failure",
		Status:    ErrorStatusActive,
		CreatedAt: time.Now().UTC(),
	})

	svc := NewService(store, nil)
	server := httptest.NewServer(svc.ErrorHandler())
	defer server.Close()

	// List active errors.
	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	var payload struct {
		Errors []map[string]any `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	_ = resp.Body.Close()
	if len(payload.Errors) != 1 || payload.Errors[0]["id"] != float64(id) || payload.Errors[0]["video_id"] != float64(vidID) || payload.Errors[0]["error_category"] != string(ErrorCategoryAPIError) || payload.Errors[0]["error_message"] != "diag:v1 phase=unknown operation=unknown code=unknown_error" {
		t.Fatalf("GET response does not match frontend queue contract: %#v", payload.Errors)
	}

	// Dismiss the error.
	req, _ := http.NewRequest("POST", server.URL+fmt.Sprintf("/%d/dismiss", id), nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST dismiss failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST dismiss status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestRetryConflictResponseUsesSafeDiagnostic(t *testing.T) {
	store := newTestStore(t)
	runID := seedRun(t, store.db)
	videoID := seedVideo(t, store.db, runID, "yt-conflict")
	id, err := store.CreateErrorEntry(context.Background(), &ErrorEntry{
		VideoID: videoID, RunID: runID, Category: ErrorCategoryRuntime,
		Message: "diag:v1 phase=unknown operation=unknown code=unknown_error",
		Status:  ErrorStatusActive, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DismissErrorEntry(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewService(store, nil).ErrorHandler())
	defer server.Close()
	resp, err := http.Post(fmt.Sprintf("%s/%d/retry", server.URL, id), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d want %d", resp.StatusCode, http.StatusConflict)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, want := body["error"], "diag:v1 phase=unknown operation=retry code=unknown_error"; got != want {
		t.Fatalf("conflict error=%q want %q", got, want)
	}
	const secret = "TOKEN_SENTINEL_plain_ascii_7341"
	if got := queueFailureMessage(fmt.Errorf("%s: %w", secret, ErrInvalidState), "retry"); got == "" || bytes.Contains([]byte(got), []byte(secret)) {
		t.Fatalf("queue failure summary leaked marker-free secret: %q", got)
	}
}

func TestDeferredQueueWithholdsInvalidLegacyReason(t *testing.T) {
	store := newTestStore(t)
	store.db.SetMaxOpenConns(1)
	runID := seedRun(t, store.db)
	videoID := seedVideo(t, store.db, runID, "yt-legacy-deferred")
	id, err := store.CreateDeferredItem(context.Background(), &DeferredItem{
		VideoID: videoID, RunID: runID, Reason: DeferredReasonQuotaExhausted,
		Status: DeferredStatusPending, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "VIDEO_SENTINEL_plain_ascii_8129"
	conn, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA ignore_check_constraints=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `UPDATE deferred_item SET deferral_reason=? WHERE id=?`, secret, id); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA ignore_check_constraints=OFF`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if items, err := store.ListDeferredItems(context.Background(), ""); err != nil {
		t.Fatalf("reading legacy invalid queue row: %v", err)
	} else if len(items) != 1 {
		t.Fatalf("legacy deferred rows=%d want 1", len(items))
	}
	server := httptest.NewServer(NewService(store, nil).DeferredHandler())
	defer server.Close()
	resp, err := http.Get(server.URL + "/?include=all")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		Deferred []map[string]any `json:"deferred"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Deferred) != 1 {
		t.Fatalf("deferred items=%d want 1", len(body.Deferred))
	}
	item := body.Deferred[0]
	if _, leaked := item["Reason"]; leaked {
		t.Fatalf("invalid legacy reason serialized: %#v", item)
	}
	if item["deferred_reason_unavailable"] != true {
		t.Fatalf("missing unavailable flag: %#v", item)
	}
	if item["Status"] != string(DeferredStatusPending) {
		t.Fatalf("status changed: %#v", item)
	}
	if bytes.Contains(mustJSON(t, item), []byte(secret)) {
		t.Fatal("legacy reason leaked")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDeferredHandlerListAndDiscard(t *testing.T) {
	store := newTestStore(t)
	db := store.db
	runID := seedRun(t, db)
	vidID := seedVideo(t, db, runID, "yt-defer-http")

	id, _ := store.CreateDeferredItem(context.Background(), &DeferredItem{
		VideoID:   vidID,
		RunID:     runID,
		Reason:    DeferredReasonQuotaExhausted,
		Status:    DeferredStatusPending,
		CreatedAt: time.Now().UTC(),
	})

	svc := NewService(store, nil)
	server := httptest.NewServer(svc.DeferredHandler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	req, _ := http.NewRequest("POST", server.URL+fmt.Sprintf("/%d/discard", id), nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST discard failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST discard status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestHandlerInvalidID(t *testing.T) {
	svc := NewService(newTestStore(t), nil)
	server := httptest.NewServer(svc.ErrorHandler())
	defer server.Close()

	resp, err := http.Post(server.URL+"/abc/retry", "", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
