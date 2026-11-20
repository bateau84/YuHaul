package queue

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
)

// Handler returns an http.Handler that serves the error queue endpoints.
// Mount this handler at /api/errors/.
func (s *Service) ErrorHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleListErrors)
	mux.HandleFunc("POST /{id}/retry", s.handleRetryError)
	mux.HandleFunc("POST /{id}/dismiss", s.handleDismissError)
	return mux
}

// DeferredHandler returns an http.Handler that serves the deferred work queue
// endpoints. Mount this handler at /api/deferred/.
func (s *Service) DeferredHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleListDeferred)
	mux.HandleFunc("POST /{id}/retry", s.handleRetryDeferred)
	mux.HandleFunc("POST /{id}/discard", s.handleDiscardDeferred)
	return mux
}

func (s *Service) handleListErrors(w http.ResponseWriter, r *http.Request) {
	include := r.URL.Query().Get("include")
	var entries []ErrorEntry
	var err error

	switch include {
	case "", "active":
		entries, err = s.ListActiveErrors(r.Context())
	case "all":
		entries, err = s.ListAllErrors(r.Context())
	default:
		writeError(w, http.StatusBadRequest, "invalid include value; use 'active' or 'all'")
		return
	}

	if err != nil {
		slog.ErrorContext(r.Context(), "list errors failed", "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to list error queue")
		return
	}

	views := publicErrorEntries(entries)
	ids := make([]string, len(views))
	for i := range views {
		ids[i] = views[i].ExternalVideoID
	}
	titles := s.localTitles(r.Context(), ids)
	for i := range views {
		views[i].VideoTitle = titles[views[i].ExternalVideoID]
	}
	// A title may expire; the browser must not keep it past that.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"errors": views})
}

func (s *Service) handleRetryError(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	entry, err := s.RetryError(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStartupRecoveryActive) {
			writeError(w, http.StatusServiceUnavailable, ErrStartupRecoveryActive.Error())
			return
		}
		if errors.Is(err, ErrShuttingDown) {
			writeError(w, http.StatusServiceUnavailable, ErrShuttingDown.Error())
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "error entry not found")
			return
		}
		var final *AlreadyFinalError
		if errors.As(err, &final) {
			s.writeAlreadyFinal(w, r, final, func() (any, error) {
				e, gerr := s.store.GetErrorEntry(r.Context(), id)
				if gerr != nil {
					return nil, gerr
				}
				return publicErrorEntry(e), nil
			})
			return
		}
		if errors.Is(err, ErrInvalidState) {
			writeError(w, http.StatusConflict, queueFailureMessage(err, "retry"))
			return
		}
		slog.ErrorContext(r.Context(), "retry error failed", "error_entry_id", id, "diagnostic", diag.Encode(err, "unknown", "retry"))
		writeError(w, http.StatusInternalServerError, "failed to retry error entry")
		return
	}

	writeJSON(w, http.StatusOK, publicErrorEntry(entry))
}

func (s *Service) handleDismissError(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	entry, err := s.DismissError(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "error entry not found")
			return
		}
		slog.ErrorContext(r.Context(), "dismiss error failed", "error_entry_id", id, "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to dismiss error entry")
		return
	}

	writeJSON(w, http.StatusOK, publicErrorEntry(entry))
}

func (s *Service) handleListDeferred(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	query := DeferredPageQuery{Limit: defaultDeferredPageLimit}
	switch values.Get("include") {
	case "", "pending":
		query.Include = DeferredStatusPending
	case "all":
	default:
		writeError(w, http.StatusBadRequest, "invalid include value; use 'pending' or 'all'")
		return
	}

	if !parseDeferredPageParams(values, &query) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid page", "code": "invalid_page"})
		return
	}
	if reason := DeferredReason(values.Get("reason")); reason != "" {
		if !reason.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reason", "code": "invalid_reason"})
			return
		}
		query.Reason = reason
	}

	page, err := s.store.ListDeferredPage(r.Context(), query)
	if err != nil {
		slog.ErrorContext(r.Context(), "list deferred failed", "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to list deferred queue")
		return
	}

	views := publicDeferredItems(page.Items)
	ids := make([]string, len(views))
	for i := range views {
		ids[i] = views[i].ExternalVideoID
	}
	titles := s.localTitles(r.Context(), ids)
	for i := range views {
		views[i].VideoTitle = titles[views[i].ExternalVideoID]
	}
	w.Header().Set("Cache-Control", "no-store")

	byReason := make(map[string]int, len(page.ByReason))
	for reason, n := range page.ByReason {
		byReason[string(reason)] = n
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deferred":    views,
		"total":       page.Total,
		"queue_total": page.QueueTotal,
		"by_reason":   byReason,
		"page": map[string]any{
			"limit":          query.Limit,
			"number":         page.Number,
			"total_pages":    page.TotalPages,
			"first_position": page.FirstPosition,
			"last_position":  page.LastPosition,
			"next_after_id":  page.NextAfterID,
			"prev_before_id": page.PrevBeforeID,
		},
	})
}

const defaultDeferredPageLimit = 50

// parseDeferredPageParams reads limit, after_id, before_id and page. It
// reports false for any malformed value or more than one selector.
func parseDeferredPageParams(values url.Values, q *DeferredPageQuery) bool {
	if values.Has("limit") {
		n, err := strconv.Atoi(values.Get("limit"))
		if err != nil || n < 1 || n > MaxDeferredPageLimit {
			return false
		}
		q.Limit = n
	}
	selectors := 0
	cursor := func(name string, dst *int64) bool {
		if !values.Has(name) {
			return true
		}
		selectors++
		id, err := strconv.ParseInt(values.Get(name), 10, 64)
		if err != nil || id <= 0 {
			return false
		}
		*dst = id
		return true
	}
	if !cursor("after_id", &q.AfterID) || !cursor("before_id", &q.BeforeID) {
		return false
	}
	if values.Has("page") {
		selectors++
		if v := values.Get("page"); v == "last" {
			q.Last = true
		} else if n, err := strconv.Atoi(v); err != nil || n < 1 {
			return false
		} else {
			q.Page = n
		}
	}
	return selectors <= 1
}

func (s *Service) handleRetryDeferred(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	item, err := s.RetryDeferred(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrStartupRecoveryActive) {
			writeError(w, http.StatusServiceUnavailable, ErrStartupRecoveryActive.Error())
			return
		}
		if errors.Is(err, ErrShuttingDown) {
			writeError(w, http.StatusServiceUnavailable, ErrShuttingDown.Error())
			return
		}
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deferred item not found")
			return
		}
		var final *AlreadyFinalError
		if errors.As(err, &final) {
			s.writeAlreadyFinal(w, r, final, func() (any, error) {
				d, gerr := s.store.GetDeferredItem(r.Context(), id)
				if gerr != nil {
					return nil, gerr
				}
				return publicDeferredItem(d), nil
			})
			return
		}
		if errors.Is(err, ErrInvalidState) {
			writeError(w, http.StatusConflict, queueFailureMessage(err, "retry"))
			return
		}
		slog.ErrorContext(r.Context(), "retry deferred failed", "deferred_item_id", id, "diagnostic", diag.Encode(err, "unknown", "retry"))
		writeError(w, http.StatusInternalServerError, "failed to retry deferred item")
		return
	}

	writeJSON(w, http.StatusOK, publicDeferredItem(item))
}

// writeAlreadyFinal answers a refused retry of an entry whose video already has
// a final outcome (ARS-006 §4): 409 with the closed entry.
func (s *Service) writeAlreadyFinal(w http.ResponseWriter, r *http.Request, final *AlreadyFinalError, load func() (any, error)) {
	entry, err := load()
	if err != nil {
		slog.ErrorContext(r.Context(), "load closed entry failed", "entry_id", final.EntryID, "diagnostic", diag.Encode(err, "unknown", "retry"))
		writeError(w, http.StatusInternalServerError, "failed to load closed entry")
		return
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":         "this video already has a final outcome; nothing was retried",
		"code":          "video_already_final",
		"final_outcome": final.Outcome,
		"entry":         entry,
	})
}

func (s *Service) handleDiscardDeferred(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r, "id")
	if !ok {
		return
	}

	item, err := s.DiscardDeferred(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "deferred item not found")
			return
		}
		slog.ErrorContext(r.Context(), "discard deferred failed", "deferred_item_id", id, "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
		writeError(w, http.StatusInternalServerError, "failed to discard deferred item")
		return
	}

	writeJSON(w, http.StatusOK, publicDeferredItem(item))
}

func parseIDParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v := r.PathValue(name)
	if strings.TrimSpace(v) == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return 0, false
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// errorEntryView is the public projection of an error queue entry. It keeps
// the frontend's snake_case wire shape and admits only canonical or exact
// legacy diagnostic text. Rejected prose is withheld; unknown_error summaries
// remain useful but set ReasonUnavailable. The stored row and retry/resolution
// lifecycle are unchanged (ARD-001).
type errorEntryView struct {
	RetryRunID        *int64        `json:"retry_run_id,omitempty"`
	ID                int64         `json:"id"`
	VideoID           int64         `json:"video_id"`
	ExternalVideoID   string        `json:"external_video_id"`
	RunID             int64         `json:"run_id"`
	Category          ErrorCategory `json:"error_category"`
	Message           string        `json:"error_message"`
	Detail            string        `json:"detail,omitempty"`
	Status            ErrorStatus   `json:"status"`
	CreatedAt         time.Time     `json:"created_at"`
	ResolvedAt        *time.Time    `json:"resolved_at,omitempty"`
	RetryCount        int           `json:"retry_count"`
	ReasonUnavailable bool          `json:"error_message_unavailable,omitempty"`
	ClosedReason      *string       `json:"closed_reason"`
	VideoFinalOutcome *string       `json:"video_final_outcome,omitempty"`
	// VideoTitle is set only by the list handlers, only from a title held
	// locally now (never stored in the queue row), and is otherwise omitted.
	VideoTitle string `json:"video_title,omitempty"`
}

// unsafeText returns text unchanged when it is safe to expose, and "" plus an
// unavailable flag when non-empty stored text fails vetting.
func publicErrorEntry(e *ErrorEntry) errorEntryView {
	view := errorEntryView{
		RetryRunID:        e.RetryRunID,
		ID:                e.ID,
		VideoID:           e.VideoID,
		ExternalVideoID:   e.ExternalVideoID,
		RunID:             e.RunID,
		Category:          e.Category,
		Status:            e.Status,
		CreatedAt:         e.CreatedAt,
		ResolvedAt:        e.ResolvedAt,
		RetryCount:        e.RetryCount,
		ClosedReason:      e.ClosedReason,
		VideoFinalOutcome: e.VideoFinalOutcome,
	}
	var unavailable bool
	view.Message, unavailable = diag.AdmitErrorText(e.Message)
	m := unavailable
	view.Detail, unavailable = diag.AdmitErrorText(e.Detail)
	view.ReasonUnavailable = m || unavailable
	return view
}

// deferredItemView is the wire shape of a deferred item. It matches
// DeferredItem in web/src/types.ts; channel_name is optional there and is not
// supplied. video_title is filled by the list handler only.
type deferredItemView struct {
	RetryRunID                *int64          `json:"retry_run_id,omitempty"`
	ID                        int64           `json:"id"`
	VideoID                   int64           `json:"video_id"`
	ExternalVideoID           string          `json:"external_video_id"`
	RunID                     int64           `json:"run_id"`
	Reason                    *DeferredReason `json:"deferral_reason,omitempty"`
	DeferredReasonUnavailable bool            `json:"deferred_reason_unavailable,omitempty"`
	RetryAfter                *time.Time      `json:"retry_after,omitempty"`
	RetryCount                int             `json:"retry_count"`
	Status                    DeferredStatus  `json:"status"`
	CreatedAt                 time.Time       `json:"created_at"`
	ClosedReason              *string         `json:"closed_reason"`
	VideoFinalOutcome         *string         `json:"video_final_outcome,omitempty"`
	VideoTitle                string          `json:"video_title,omitempty"`
}

func publicDeferredItem(item *DeferredItem) deferredItemView {
	if item == nil {
		return deferredItemView{}
	}
	view := deferredItemView{
		RetryRunID:        item.RetryRunID,
		ID:                item.ID,
		VideoID:           item.VideoID,
		ExternalVideoID:   item.ExternalVideoID,
		RunID:             item.RunID,
		RetryAfter:        item.RetryAfter,
		RetryCount:        item.RetryCount,
		Status:            item.Status,
		CreatedAt:         item.CreatedAt,
		ClosedReason:      item.ClosedReason,
		VideoFinalOutcome: item.VideoFinalOutcome,
	}
	if item.Reason == "" {
		return view
	}
	if value, ok := diag.AdmitDisposition(string(item.Reason), "queue_reason"); ok {
		reason := DeferredReason(value)
		view.Reason = &reason
	} else {
		view.DeferredReasonUnavailable = true
	}
	return view
}

// localTitles returns the titles that are held locally right now, by external
// video ID. It reads in chunks of 100 (the reader's limit) and uses a title only
// when its availability is current_local. A failed read yields no titles for the
// whole list: the list is still served and the UI falls back to the video ID.
func (s *Service) localTitles(ctx context.Context, ids []string) map[string]string {
	if s.titles == nil {
		return nil
	}
	const chunk = 100
	out := make(map[string]string)
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	for start := 0; start < len(unique); start += chunk {
		end := min(start+chunk, len(unique))
		display, err := s.titles.ReadLocalDisplay(ctx, unique[start:end])
		if err != nil {
			slog.WarnContext(ctx, "queue list titles unavailable", "diagnostic", diag.Encode(err, "unknown", "inspect_queue"))
			return nil
		}
		for id, d := range display {
			if d.TitleAvailability == "current_local" && d.Title != nil && *d.Title != "" {
				out[id] = *d.Title
			}
		}
	}
	return out
}

func publicDeferredItems(items []DeferredItem) []deferredItemView {
	out := make([]deferredItemView, len(items))
	for i := range items {
		out[i] = publicDeferredItem(&items[i])
	}
	return out
}

func publicErrorEntries(entries []ErrorEntry) []errorEntryView {
	out := make([]errorEntryView, len(entries))
	for i := range entries {
		out[i] = publicErrorEntry(&entries[i])
	}
	return out
}

func queueFailureMessage(err error, operation string) string {
	return diag.Encode(err, "unknown", operation)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		_ = err
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
