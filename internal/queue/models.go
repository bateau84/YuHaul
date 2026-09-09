// Package queue implements YuHaul's error queue and deferred work queue.
//
// Error entries record processing failures that require user attention or
// automatic retry. Deferred items record work that was postponed, usually
// because of quota exhaustion or rate limiting. Both queues preserve history
// when entries are dismissed or discarded.
package queue

import "time"

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
	DeferredReasonQuotaExhausted DeferredReason = "quota_exhausted"
	DeferredReasonAPIRateLimited DeferredReason = "api_rate_limited"
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
}

// DeferredItem is a single unit of work held for later processing.
type DeferredItem struct {
	ID              int64
	VideoID         int64
	ExternalVideoID string
	RunID           int64
	Reason          DeferredReason
	RetryAfter      *time.Time
	RetryCount      int
	Status          DeferredStatus
	CreatedAt       time.Time
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
	case DeferredReasonQuotaExhausted, DeferredReasonAPIRateLimited:
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
