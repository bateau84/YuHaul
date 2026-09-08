package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
)

// SettingsReader reads effective-configuration settings.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// Service computes analytics views from raw pipeline data.
type Service struct {
	store    *Store
	settings SettingsReader
	ledger   *quota.Ledger
}

// NewService returns an analytics service. ledger may be nil; quota endpoints
// will return an error in that case.
func NewService(store *Store, settings SettingsReader, ledger *quota.Ledger) *Service {
	return &Service{store: store, settings: settings, ledger: ledger}
}

// RunStats returns statistics for a run.
func (s *Service) RunStats(ctx context.Context, runID int64) (*RunStats, error) {
	return s.store.GetRunStats(ctx, runID)
}

// RouteStats returns per-route statistics for a run.
func (s *Service) RouteStats(ctx context.Context, runID int64) ([]RouteStats, error) {
	return s.store.ListRouteStats(ctx, runID)
}

// ChannelStats returns statistics for all channels.
func (s *Service) ChannelStats(ctx context.Context) ([]ChannelStats, error) {
	return s.store.ListChannelStats(ctx)
}

// ChannelDetail returns statistics for one channel.
func (s *Service) ChannelDetail(ctx context.Context, channelIDText string) (*ChannelStats, error) {
	cs, err := s.store.GetChannelStats(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	// Enrich with duplicate rate computed from source data.
	dp, err := s.store.ListDuplicateProvenance(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	if cs.TotalDiscovered > 0 {
		cs.DuplicateRate = float64(len(dp)) / float64(cs.TotalDiscovered)
	}
	return cs, nil
}

// TemporalStats returns time-bucketed run statistics.
func (s *Service) TemporalStats(ctx context.Context, from, to time.Time, granularity string) ([]TemporalBucket, error) {
	return s.store.GetTemporalStats(ctx, from, to, granularity)
}

// DuplicateProvenance returns duplicate credit breakdown for a channel.
func (s *Service) DuplicateProvenance(ctx context.Context, channelIDText string) ([]DuplicateProvenance, error) {
	return s.store.ListDuplicateProvenance(ctx, channelIDText)
}

// RetentionSignals returns retention signal observations for a channel.
func (s *Service) RetentionSignals(ctx context.Context, channelIDText string) ([]RetentionSignal, error) {
	return s.store.ListRetentionSignals(ctx, channelIDText)
}

// RetentionPlaylists lists designated retention playlists.
func (s *Service) RetentionPlaylists(ctx context.Context) ([]RetentionPlaylist, error) {
	return s.store.ListRetentionPlaylists(ctx)
}

// DesignateRetentionPlaylist marks a playlist as a retention signal source.
func (s *Service) DesignateRetentionPlaylist(ctx context.Context, playlistID, playlistName string) (*RetentionPlaylist, error) {
	if playlistID == "" {
		return nil, fmt.Errorf("stats: playlist_id is required")
	}
	return s.store.CreateRetentionPlaylist(ctx, playlistID, playlistName)
}

// RemoveRetentionPlaylist removes a retention playlist designation.
func (s *Service) RemoveRetentionPlaylist(ctx context.Context, id int64) error {
	return s.store.DeleteRetentionPlaylist(ctx, id)
}

// RecordRetentionSignal records a retention signal observation and refreshes
// the channel's total_retained counter.
func (s *Service) RecordRetentionSignal(ctx context.Context, videoID, playlistID, membershipSource string) error {
	isSignal := membershipSource == "user_driven"
	if err := s.store.RecordRetentionSignal(ctx, videoID, playlistID, membershipSource, isSignal); err != nil {
		return err
	}
	// We do not know the channel from the signal alone; refresh is best-effort
	// when the caller supplies it. The counter is reconcilable on demand.
	return nil
}

// RefreshChannelRetained recomputes total_retained for a channel.
func (s *Service) RefreshChannelRetained(ctx context.Context, channelIDText string) error {
	return s.store.UpdateChannelRetained(ctx, channelIDText)
}

// QuotaStatus returns the current quota budget plus a per-operation breakdown.
func (s *Service) QuotaStatus(ctx context.Context) (*quota.BudgetSnapshot, []QuotaBreakdown, error) {
	if s.ledger == nil {
		return nil, nil, fmt.Errorf("stats: quota status requires a quota ledger")
	}
	budget, err := s.ledger.Budget(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("stats: quota budget: %w", err)
	}

	rows, err := s.store.db.QueryContext(ctx,
		`SELECT date, operation_type, SUM(units_consumed), SUM(call_count)
		 FROM quota_ledger
		 WHERE date = ?
		 GROUP BY date, operation_type
		 ORDER BY operation_type;`, budget.Date)
	if err != nil {
		return nil, nil, fmt.Errorf("stats: quota breakdown: %w", err)
	}
	defer rows.Close()

	var breakdown []QuotaBreakdown
	for rows.Next() {
		var q QuotaBreakdown
		if err := rows.Scan(&q.Date, &q.Operation, &q.Units, &q.CallCount); err != nil {
			return nil, nil, fmt.Errorf("stats: scan quota breakdown: %w", err)
		}
		breakdown = append(breakdown, q)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("stats: iterate quota breakdown: %w", err)
	}
	return budget, breakdown, nil
}
