package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
)

// DefaultTTL is the default metadata retention window (30 days).
const DefaultTTL = 30 * 24 * time.Hour

// DefaultRefreshBatchSize is the number of video IDs requested per refresh batch.
const DefaultRefreshBatchSize = 50

// Manager refreshes active Store A records and expires stale ones.
type Manager struct {
	store     *Store
	fetcher   Fetcher
	ledger    *quota.Ledger
	logger    *slog.Logger
	ttl       time.Duration
	batchSize int
	now       func() time.Time
}

// Option configures a Manager.
type Option func(*Manager)

// WithLogger sets the logger used by the manager.
func WithLogger(logger *slog.Logger) Option {
	return func(m *Manager) {
		if logger != nil {
			m.logger = logger
		}
	}
}

// WithTTL sets the retention window for refreshed metadata.
func WithTTL(ttl time.Duration) Option {
	return func(m *Manager) {
		if ttl > 0 {
			m.ttl = ttl
		}
	}
}

// WithBatchSize sets the number of video IDs passed to the fetcher at once.
func WithBatchSize(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.batchSize = n
		}
	}
}

// WithNow sets the clock function used for time-sensitive operations.
func WithNow(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// NewManager returns a lifecycle manager backed by store and ledger.
// The fetcher is used for both scheduled and on-demand refresh.
func NewManager(store *Store, fetcher Fetcher, ledger *quota.Ledger, opts ...Option) *Manager {
	m := &Manager{
		store:     store,
		fetcher:   fetcher,
		ledger:    ledger,
		logger:    slog.Default(),
		ttl:       DefaultTTL,
		batchSize: DefaultRefreshBatchSize,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// RunLifecycle expires stale records and refreshes active routed records that
// are nearing expiry. It is safe to call repeatedly (e.g., at the start of each
// processing run). The hybrid policy implemented here refreshes routed videos
// automatically; older/unrouted videos are refreshed only on demand.
func (m *Manager) RunLifecycle(ctx context.Context) error {
	expired, err := m.ExpireStale(ctx)
	if err != nil {
		return err
	}
	m.logger.InfoContext(ctx, "metadata lifecycle expired stale records", "count", expired)

	refreshed, err := m.RefreshRoutedNearExpiry(ctx)
	if err != nil {
		return err
	}
	m.logger.InfoContext(ctx, "metadata lifecycle refreshed routed records", "count", refreshed)
	return nil
}

// CleanupExpired deletes metadata records older than the configured TTL.
// It performs only local DELETE operations and never consumes API quota.
// Returns the number of records deleted.
func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
	cutoff := m.now().UTC().Add(-m.ttl)
	return m.store.CleanupExpired(ctx, cutoff, 10000)
}

// ExpireStale marks active records past expires_at as expired, clearing raw
// metadata fields. It returns the number of records expired.
func (m *Manager) ExpireStale(ctx context.Context) (int, error) {
	now := m.now().UTC()
	ids, err := m.store.ListActivePastExpiry(ctx, now, 10000)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := m.store.Expire(ctx, ids, now); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// RefreshRoutedNearExpiry refreshes active records for routed videos whose
// expiry is within the next 7 days. It returns the number of records refreshed.
func (m *Manager) RefreshRoutedNearExpiry(ctx context.Context) (int, error) {
	const window = 7 * 24 * time.Hour
	return m.refreshCandidates(ctx, func(ctx context.Context, cutoff time.Time, limit int) ([]RefreshCandidate, error) {
		return m.store.ListActiveRoutedBefore(ctx, cutoff, limit)
	}, window)
}

// RefreshActiveNearExpiry refreshes all active records nearing expiry,
// regardless of routing status. Prefer RefreshRoutedNearExpiry for the hybrid
// policy; this is useful for tests and backfills.
func (m *Manager) RefreshActiveNearExpiry(ctx context.Context) (int, error) {
	const window = 7 * 24 * time.Hour
	return m.refreshCandidates(ctx, m.store.ListActiveBefore, window)
}

// RefreshOnDemand re-fetches metadata for a single video and restores display.
// The provider adapter records actual calls in the ledger.
func (m *Manager) RefreshOnDemand(ctx context.Context, videoID string) error {
	cost := quota.OpVideosList.UnitCost()
	ok, _, err := m.ledger.CanAfford(ctx, cost)
	if err != nil {
		return fmt.Errorf("metadata: check quota for on-demand refresh: %w", err)
	}
	if !ok {
		m.logger.WarnContext(ctx, "on-demand metadata refresh deferred: quota exhausted")
		return nil
	}

	fresh, err := m.fetcher.FetchMetadata(ctx, []string{videoID})
	if err != nil {
		return fmt.Errorf("metadata: fetch on-demand refresh for %s: %w", videoID, err)
	}
	if len(fresh) == 0 {
		return fmt.Errorf("metadata: on-demand refresh for %s returned no data", videoID)
	}

	now := m.now().UTC()
	if err := m.store.Refresh(ctx, fresh, m.ttl, now); err != nil {
		return err
	}

	return nil
}

// ReadLocalDisplay performs a local-only, bounded metadata read for run members.
func (m *Manager) ReadLocalDisplay(ctx context.Context, ids []string) (map[string]LocalDisplay, error) {
	if len(ids) > 100 {
		return nil, fmt.Errorf("metadata: local display batch exceeds 100 IDs")
	}
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			unique = append(unique, id)
		}
	}
	rows, err := m.store.readLocalDisplayRows(ctx, unique)
	if err != nil {
		return nil, err
	}
	// One cutoff is captured only after the database result set has been fully consumed.
	cutoff := m.now().UTC()
	out := make(map[string]LocalDisplay, len(unique))
	for _, id := range unique {
		out[id] = LocalDisplay{Attribution: "current_local_not_necessarily_evaluated", TitleAvailability: "unknown", ChannelNameAvailability: "unknown", DurationAvailability: "unknown"}
	}
	for _, row := range rows {
		out[row.VideoID] = ProjectLocal(row, cutoff)
	}
	return out, nil
}

// ParseLocalTime parses a stored timestamp in any layout the schema has used.
// Zero and unparseable values report false.
func ParseLocalTime(value any) (time.Time, bool) {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return time.Time{}, false
		}
		return v.UTC(), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04:05"} {
			if parsed, err := time.Parse(layout, v); err == nil && !parsed.IsZero() {
				return parsed.UTC(), true
			}
		}
	case []byte:
		return ParseLocalTime(string(v))
	}
	return time.Time{}, false
}

// GetDisplayMetadata returns active metadata for a video. If the record has
// expired, it returns [MetadataExpiredError] so the caller can degrade to the
// Store B fingerprint.
func (m *Manager) GetDisplayMetadata(ctx context.Context, videoID string) (*TransientMetadata, error) {
	meta, err := m.store.Get(ctx, videoID)
	if err != nil {
		return nil, err
	}
	now := m.now().UTC()
	if !meta.IsActive || meta.ExpiredAt != nil || meta.ExpiresAt.Before(now) {
		return nil, &MetadataExpiredError{VideoID: videoID}
	}
	return meta, nil
}

// GetDisplayTitle returns the best available human-readable title for a video.
// Active metadata is preferred. If the metadata has expired or is missing, the
// title degrades to the Store B fingerprint hash. If no fingerprint exists, a
// fixed degraded label is returned.
func (m *Manager) GetDisplayTitle(ctx context.Context, videoID string) (string, error) {
	meta, err := m.store.Get(ctx, videoID)
	if err == nil {
		now := m.now().UTC()
		if meta.IsActive && meta.ExpiredAt == nil && !meta.ExpiresAt.Before(now) && meta.Title != nil {
			return *meta.Title, nil
		}
	}

	fp, err := m.store.GetFingerprint(ctx, videoID)
	if err != nil {
		return "", err
	}
	if fp != "" {
		return fp, nil
	}
	return "[metadata expired]", nil
}

// CountStaleActive returns the number of active records whose expires_at is
// before now. It is used by the falsification test for the 30-day TTL.
func (m *Manager) CountStaleActive(ctx context.Context) (int, error) {
	now := m.now().UTC()
	var count int
	err := m.store.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM transient_metadata
		WHERE is_active = 1 AND expires_at < ?;`, now).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("metadata: count stale active: %w", err)
	}
	return count, nil
}

type candidateSource func(ctx context.Context, cutoff time.Time, limit int) ([]RefreshCandidate, error)

func (m *Manager) refreshCandidates(ctx context.Context, src candidateSource, window time.Duration) (int, error) {
	now := m.now().UTC()
	cutoff := now.Add(window)

	candidates, err := src(ctx, cutoff, 10000)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, nil
	}

	refreshed := 0
	for i := 0; i < len(candidates); i += m.batchSize {
		end := i + m.batchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[i:end]
		ids := make([]string, len(batch))
		for j, c := range batch {
			ids[j] = c.VideoID
		}

		cost := quota.OpVideosList.UnitCost()
		ok, _, err := m.ledger.CanAfford(ctx, cost)
		if err != nil {
			return refreshed, fmt.Errorf("metadata: check quota for refresh: %w", err)
		}
		if !ok {
			m.logger.WarnContext(ctx, "metadata refresh deferred: quota exhausted",
				"batch_size", len(ids))
			return refreshed, nil
		}

		fresh, err := m.fetcher.FetchMetadata(ctx, ids)
		if err != nil {
			return refreshed, fmt.Errorf("metadata: fetch refresh batch: %w", err)
		}

		if err := m.store.Refresh(ctx, fresh, m.ttl, now); err != nil {
			return refreshed, err
		}

		refreshed += len(fresh)
	}
	return refreshed, nil
}

// IsMetadataExpired reports whether err is a metadata-expired error.
func IsMetadataExpired(err error) bool {
	var expired *MetadataExpiredError
	return errors.As(err, &expired)
}
