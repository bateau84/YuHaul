package stats

import (
	"context"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/advisory"
	"github.com/bateau84/YuHaul/internal/quota"
)

// SettingsReader reads effective-configuration settings.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// AdvisoryReader returns advisories for a channel. It is implemented by
// *advisory.Store.
type AdvisoryReader interface {
	ListAdvisoriesForChannel(ctx context.Context, channelIDText string) ([]advisory.Advisory, error)
	ListActiveAdvisories(ctx context.Context) ([]advisory.Advisory, error)
}

// Service computes analytics views from raw pipeline data.
type Service struct {
	store      *Store
	settings   SettingsReader
	ledger     *quota.Ledger
	advisories AdvisoryReader
}

// NewService returns an analytics service. ledger may be nil; quota endpoints
// will return an error in that case.
func NewService(store *Store, settings SettingsReader, ledger *quota.Ledger) *Service {
	return &Service{store: store, settings: settings, ledger: ledger}
}

// WithAdvisories wires an advisory reader into the service so channel detail
// responses can include advisories.
func (s *Service) WithAdvisories(reader AdvisoryReader) *Service {
	s.advisories = reader
	return s
}

// RunStats returns statistics for a run.
func (s *Service) RunStats(ctx context.Context, runID int64) (*RunStats, error) {
	return s.store.GetRunStats(ctx, runID)
}

// RouteStats returns per-route statistics for a run.
func (s *Service) RouteStats(ctx context.Context, runID int64) ([]RouteStats, error) {
	return s.store.ListRouteStats(ctx, runID)
}

// ChannelStats returns statistics for all channels, each enriched with the
// channel's active advisories.
func (s *Service) ChannelStats(ctx context.Context) ([]ChannelStats, error) {
	list, err := s.store.ListChannelStats(ctx)
	if err != nil {
		return nil, err
	}
	if s.advisories == nil || len(list) == 0 {
		return list, nil
	}
	active, err := s.advisories.ListActiveAdvisories(ctx)
	if err != nil {
		return nil, err
	}
	byChannel := make(map[string][]AdvisorySummary)
	for _, a := range active {
		byChannel[a.ChannelIDText] = append(byChannel[a.ChannelIDText], summarizeAdvisory(a, a.ChannelIDText, a.ChannelName))
	}
	for i := range list {
		list[i].Advisories = byChannel[list[i].ChannelID]
	}
	return list, nil
}

// ChannelDetail returns statistics for one channel.
func (s *Service) ChannelDetail(ctx context.Context, channelIDText string) (*ChannelDetail, error) {
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

	dupBreakdown, err := s.store.DuplicateProvenanceBreakdown(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	retBreakdown, err := s.store.RetentionSignalBreakdown(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	matchedRoutes, err := s.store.MatchedRoutesForChannel(ctx, channelIDText)
	if err != nil {
		return nil, err
	}

	recentVideos, err := s.store.ListRecentVideos(ctx, channelIDText)
	if err != nil {
		return nil, err
	}

	provenance, err := s.store.ListRecentVideoProvenance(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	recentIDs := make(map[string]bool, len(recentVideos))
	for _, v := range recentVideos {
		recentIDs[v.VideoID] = true
	}
	recentProvenance := make([]VideoProvenance, 0, len(provenance))
	for _, p := range provenance {
		if recentIDs[p.VideoID] {
			recentProvenance = append(recentProvenance, p)
		}
	}

	advisories, err := s.listAdvisoriesForChannel(ctx, channelIDText)
	if err != nil {
		return nil, err
	}

	return &ChannelDetail{
		ChannelStats:          *cs,
		DuplicateProvenance:   dupBreakdown,
		RetentionSignals:      retBreakdown,
		RecentVideos:          recentVideos,
		RecentVideoProvenance: recentProvenance,
		MatchedRoutes:         matchedRoutes,
		Advisories:            advisories,
	}, nil
}

func (s *Service) listAdvisoriesForChannel(ctx context.Context, channelIDText string) ([]AdvisorySummary, error) {
	if s.advisories == nil {
		return make([]AdvisorySummary, 0), nil
	}
	list, err := s.advisories.ListAdvisoriesForChannel(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	out := make([]AdvisorySummary, len(list))
	for i, a := range list {
		out[i] = summarizeAdvisory(a, channelIDText, "")
	}
	return out, nil
}

// summarizeAdvisory maps an advisory to its channel-view representation. The
// channel fields default to what the advisory row itself carries so callers
// can override them with channel context they already loaded.
func summarizeAdvisory(a advisory.Advisory, channelID, channelName string) AdvisorySummary {
	if channelID == "" {
		channelID = a.ChannelIDText
	}
	if channelName == "" {
		channelName = a.ChannelName
	}
	return AdvisorySummary{
		ID:               a.ID,
		ChannelID:        channelID,
		ChannelName:      channelName,
		AdvisoryType:     string(a.Type),
		Status:           string(a.Status),
		GeneratedAt:      a.GeneratedAt,
		IgnoredUntil:     a.IgnoredUntil,
		ActionTaken:      a.ActionTaken,
		EvidenceSnapshot: a.EvidenceSnapshot,
	}
}

// TemporalStats returns time-bucketed run statistics.
func (s *Service) TemporalStats(ctx context.Context, from, to time.Time, granularity string) ([]TemporalBucket, error) {
	return s.store.GetTemporalStats(ctx, from, to, granularity)
}

// RouteMatchRates returns time-bucketed per-route match rates.
func (s *Service) RouteMatchRates(ctx context.Context, from, to time.Time, granularity string) ([]RouteMatchRate, error) {
	return s.store.GetRouteMatchRates(ctx, from, to, granularity)
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

// ListChannelStats implements the advisory.ChannelReader interface so the
// advisory generator can consume channel statistics without a separate adapter.
func (s *Service) ListChannelStats(ctx context.Context) ([]advisory.ChannelStats, error) {
	cs, err := s.store.ListChannelStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]advisory.ChannelStats, len(cs))
	for i, c := range cs {
		out[i] = advisory.ChannelStats{
			ChannelID:        c.ChannelID,
			ChannelName:      c.ChannelName,
			TotalDiscovered:  c.TotalDiscovered,
			TotalRouted:      c.TotalRouted,
			TotalRetained:    c.TotalRetained,
			LastDiscoveredAt: c.LastDiscoveredAt,
		}
	}
	return out, nil
}

// ListDuplicateProvenance implements the advisory.ChannelReader interface.
func (s *Service) ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]advisory.DuplicateProvenance, error) {
	dp, err := s.store.ListDuplicateProvenance(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	out := make([]advisory.DuplicateProvenance, len(dp))
	for i, d := range dp {
		out[i] = advisory.DuplicateProvenance{
			VideoID: d.VideoID,
		}
	}
	return out, nil
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

	breakdown := make([]QuotaBreakdown, 0)
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
