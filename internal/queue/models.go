// Package queue implements YuHaul's error queue and deferred work queue.
//
// Error entries record processing failures that require user attention or
// automatic retry. Deferred items record work that was postponed, usually
// because of quota exhaustion or rate limiting. Both queues preserve history
// when entries are dismissed or discarded.
package queue

import (
	"errors"
	"fmt"
	"time"
)

// ErrorCategory classifies the origin of a processing failure.
type ErrorCategory string

const (
	ErrorCategoryAPIError      ErrorCategory = "api_error"
	ErrorCategoryAuthorization ErrorCategory = "authorization"
	ErrorCategoryRuntime       ErrorCategory = "runtime"
)

// ErrorStatus records the lifecycle of an error queue entry.
type ErrorStatus string

const (
	ErrorStatusActive    ErrorStatus = "active"
	ErrorStatusRetrying  ErrorStatus = "retrying"
	ErrorStatusDismissed ErrorStatus = "dismissed"
	ErrorStatusResolved  ErrorStatus = "resolved"
)

// DeferredReason explains why work was deferred.
type DeferredReason string

const (
	DeferredReasonQuotaExhausted       DeferredReason = "quota_exhausted"
	DeferredReasonAPIRateLimited       DeferredReason = "api_rate_limited"
	DeferredReasonAuthorizationPending DeferredReason = "authorization_pending"
	DeferredReasonStoppedByUser        DeferredReason = "stopped_by_user"
	DeferredReasonDiscardedByUser      DeferredReason = "discarded_by_user"
	// DeferredReasonRequeuedByUser marks a stranded video the user re-queued
	// (ARS-005). HandOffUnfinishedTx never writes it.
	DeferredReasonRequeuedByUser DeferredReason = "requeued_by_user"
)

// DeferredStatus records the lifecycle of a deferred work item.
type DeferredStatus string

const (
	DeferredStatusPending   DeferredStatus = "pending"
	DeferredStatusRetrying  DeferredStatus = "retrying"
	DeferredStatusCompleted DeferredStatus = "completed"
	DeferredStatusDiscarded DeferredStatus = "discarded"
)

// ErrorEntry is a single inspectable failure record in the error queue.
type ErrorEntry struct {
	RetryRunID      *int64
	ID              int64
	VideoID         int64
	ExternalVideoID string
	RunID           int64
	Category        ErrorCategory
	Message         string
	Detail          string
	Status          ErrorStatus
	CreatedAt       time.Time
	ResolvedAt      *time.Time
	RetryCount      int
	// ClosedReason is nil, or ClosedReasonFinalOutcome when the entry was closed
	// because its video reached a final outcome (BR-039, ARS-006 §1.3).
	ClosedReason *string
	// VideoFinalOutcome is the video outcome, set only when ClosedReason is set.
	VideoFinalOutcome *string
}

// DeferredItem is a single unit of work held for later processing.
type DeferredItem struct {
	RetryRunID        *int64
	ID                int64
	VideoID           int64
	ExternalVideoID   string
	RunID             int64
	Reason            DeferredReason
	RetryAfter        *time.Time
	RetryCount        int
	Status            DeferredStatus
	CreatedAt         time.Time
	ClosedReason      *string
	VideoFinalOutcome *string
}

// Valid reports whether c is a known error category.
func (c ErrorCategory) Valid() bool {
	switch c {
	case ErrorCategoryAPIError, ErrorCategoryAuthorization, ErrorCategoryRuntime:
		return true
	}
	return false
}

// Valid reports whether s is a known error status.
func (s ErrorStatus) Valid() bool {
	switch s {
	case ErrorStatusActive, ErrorStatusRetrying, ErrorStatusDismissed, ErrorStatusResolved:
		return true
	}
	return false
}

// Valid reports whether r is a known deferred reason.
func (r DeferredReason) Valid() bool {
	switch r {
	case DeferredReasonQuotaExhausted, DeferredReasonAPIRateLimited, DeferredReasonAuthorizationPending,
		DeferredReasonStoppedByUser, DeferredReasonDiscardedByUser, DeferredReasonRequeuedByUser:
		return true
	}
	return false
}

// Valid reports whether s is a known deferred status.
func (s DeferredStatus) Valid() bool {
	switch s {
	case DeferredStatusPending, DeferredStatusRetrying, DeferredStatusCompleted, DeferredStatusDiscarded:
		return true
	}
	return false
}

// ClosedReasonFinalOutcome is the only value written to closed_reason: the
// entry was closed because its video already has a final outcome (BR-039,
// ARS-006 §1.3). Nothing else writes the column.
const ClosedReasonFinalOutcome = "final_outcome"

// ErrVideoAlreadyFinal is matched with errors.Is against an *AlreadyFinalError.
var ErrVideoAlreadyFinal = errors.New("queue: video already has a final outcome")

// AlreadyFinalError reports that a retry was refused because the entry's video
// has a final outcome. The entry's family has been closed (ARS-006 §4).
type AlreadyFinalError struct {
	VideoID, EntryID int64
	ErrorQueue       bool
	Outcome          string
}

func (e *AlreadyFinalError) Error() string {
	return fmt.Sprintf("video %d already has final outcome %q; entry %d closed", e.VideoID, e.Outcome, e.EntryID)
}

// Is reports whether target is ErrVideoAlreadyFinal.
func (e *AlreadyFinalError) Is(target error) bool { return target == ErrVideoAlreadyFinal }
