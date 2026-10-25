package queue

import (
	"context"
	"fmt"
	"log/slog"
)

// RetryHandler is invoked when a queue entry is manually retried. It performs
// the actual reprocessing. The queue service updates the entry status based on
// whether the handler returns an error.
type RetryHandler interface {
	// HandleErrorRetry reprocesses a video that previously failed.
	// If it returns nil, the error entry is marked resolved.
	HandleErrorRetry(ctx context.Context, videoID int64, externalVideoID string) error

	// HandleDeferredRetry reprocesses a video that was previously deferred.
	// If it returns nil, the deferred item is marked completed.
	HandleDeferredRetry(ctx context.Context, videoID int64, externalVideoID string) error
}

// Service orchestrates error and deferred queue operations.
type Service struct {
	store       *Store
	handler     RetryHandler
	logger      *slog.Logger
	coordinator RetryCoordinator
}

// RetryCoordinator atomically creates a receiving run and claims the video
// group, then persists its disposition before acknowledging queue work.
type RetryCoordinator interface {
	RetryQueueEntry(context.Context, int64, bool) error
}

// WithRetryCoordinator delegates ownership and disposition to the production
// pipeline instead of the legacy video-only callback protocol.
func (s *Service) WithRetryCoordinator(c RetryCoordinator) *Service {
	s.coordinator = c
	return s
}

// NewService returns a queue service backed by store. handler may be nil if
// retry requests should only update queue state and rely on a scheduler or
// downstream component for actual reprocessing.
func NewService(store *Store, handler RetryHandler) *Service {
	if store == nil {
		panic("queue: store is required")
	}
	return &Service{
		store:   store,
		handler: handler,
		logger:  slog.Default(),
	}
}

// WithLogger sets the logger used by the service.
func (s *Service) WithLogger(logger *slog.Logger) *Service {
	if logger != nil {
		s.logger = logger
	}
	return s
}

// ListActiveErrors returns inspectable active error queue entries.
func (s *Service) ListActiveErrors(ctx context.Context) ([]ErrorEntry, error) {
	return s.store.ListErrorEntries(ctx, ErrorStatusActive)
}

// ListAllErrors returns every error entry regardless of status, supporting
// historical inspection.
func (s *Service) ListAllErrors(ctx context.Context) ([]ErrorEntry, error) {
	return s.store.ListErrorEntries(ctx, "")
}

// DismissError marks an error entry as dismissed. The row is preserved.
func (s *Service) DismissError(ctx context.Context, id int64) (*ErrorEntry, error) {
	if err := s.store.DismissErrorEntry(ctx, id); err != nil {
		return nil, err
	}
	return s.store.GetErrorEntry(ctx, id)
}

// RetryError delegates the complete lifecycle to the coordinator when wired.
// Legacy callers without one use the video-only handler/status protocol.
func (s *Service) RetryError(ctx context.Context, id int64) (*ErrorEntry, error) {
	if s.coordinator != nil {
		if err := s.coordinator.RetryQueueEntry(ctx, id, true); err != nil {
			return nil, err
		}
		return s.store.GetErrorEntry(ctx, id)
	}
	entry, err := s.store.GetErrorEntry(ctx, id)
	if err != nil {
		return nil, err
	}

	if entry.Status == ErrorStatusDismissed || entry.Status == ErrorStatusResolved {
		return nil, fmt.Errorf("error entry %d has status %q and cannot be retried: %w", id, entry.Status, ErrInvalidState)
	}

	if err := s.store.SetErrorEntryRetrying(ctx, id); err != nil {
		return nil, err
	}

	if s.handler == nil {
		return s.store.GetErrorEntry(ctx, id)
	}

	if err := s.handler.HandleErrorRetry(ctx, entry.VideoID, entry.ExternalVideoID); err != nil {
		s.logger.WarnContext(ctx, "error retry handler failed, re-recording failure",
			"error_entry_id", id,
			"video_id", entry.ExternalVideoID,
			"error", err,
		)
		if uerr := s.store.UpdateErrorEntryFailure(ctx, id, err.Error()); uerr != nil {
			return nil, fmt.Errorf("handler failed (%v) and status update failed: %w", err, uerr)
		}
		return s.store.GetErrorEntry(ctx, id)
	}

	if err := s.store.ResolveErrorEntry(ctx, id); err != nil {
		return nil, err
	}
	return s.store.GetErrorEntry(ctx, id)
}

// ListPendingDeferred returns deferred work items that are eligible for retry.
func (s *Service) ListPendingDeferred(ctx context.Context) ([]DeferredItem, error) {
	return s.store.ListDeferredItems(ctx, DeferredStatusPending)
}

// ListAllDeferred returns every deferred item regardless of status, supporting
// historical inspection.
func (s *Service) ListAllDeferred(ctx context.Context) ([]DeferredItem, error) {
	return s.store.ListDeferredItems(ctx, "")
}

// RetryDeferred delegates the complete lifecycle to the coordinator when wired.
// Legacy callers without one use the video-only handler/status protocol.
func (s *Service) RetryDeferred(ctx context.Context, id int64) (*DeferredItem, error) {
	if s.coordinator != nil {
		if err := s.coordinator.RetryQueueEntry(ctx, id, false); err != nil {
			return nil, err
		}
		return s.store.GetDeferredItem(ctx, id)
	}
	item, err := s.store.GetDeferredItem(ctx, id)
	if err != nil {
		return nil, err
	}

	if item.Status == DeferredStatusCompleted || item.Status == DeferredStatusDiscarded {
		return nil, fmt.Errorf("deferred item %d has status %q and cannot be retried: %w", id, item.Status, ErrInvalidState)
	}

	if err := s.store.MarkDeferredRetrying(ctx, id); err != nil {
		return nil, err
	}

	if s.handler == nil {
		return s.store.GetDeferredItem(ctx, id)
	}

	if err := s.handler.HandleDeferredRetry(ctx, item.VideoID, item.ExternalVideoID); err != nil {
		s.logger.WarnContext(ctx, "deferred retry handler failed, returning to pending",
			"deferred_item_id", id,
			"video_id", item.ExternalVideoID,
			"error", err,
		)
		if uerr := s.store.MarkDeferredPending(ctx, id); uerr != nil {
			return nil, fmt.Errorf("handler failed (%v) and status update failed: %w", err, uerr)
		}
		return s.store.GetDeferredItem(ctx, id)
	}

	if err := s.store.MarkDeferredCompleted(ctx, id); err != nil {
		return nil, err
	}
	return s.store.GetDeferredItem(ctx, id)
}

// DiscardDeferred marks a deferred item as discarded. The row is preserved.
func (s *Service) DiscardDeferred(ctx context.Context, id int64) (*DeferredItem, error) {
	if err := s.store.DiscardDeferredItem(ctx, id); err != nil {
		return nil, err
	}
	return s.store.GetDeferredItem(ctx, id)
}

// ErrInvalidState indicates that a requested operation is incompatible with the
// current queue entry status.
var ErrInvalidState = fmt.Errorf("queue: invalid entry state")
