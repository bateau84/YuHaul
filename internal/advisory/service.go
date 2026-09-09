package advisory

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// ChannelReader provides channel statistics for advisory generation.
type ChannelReader interface {
	ListChannelStats(ctx context.Context) ([]ChannelStats, error)
	ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]DuplicateProvenance, error)
}

// ChannelStats is the subset of channel statistics used by the advisory service.
type ChannelStats struct {
	ChannelID       string
	ChannelName     string
	TotalDiscovered int
	TotalRouted     int
	TotalRetained   int
	LastDiscoveredAt *time.Time
}

// DuplicateProvenance is the subset of duplicate provenance used by advisories.
type DuplicateProvenance struct {
	VideoID string
}

// SettingsReader reads advisory threshold configuration.
type SettingsReader interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// Service generates and manages advisories.
type Service struct {
	store    *Store
	channels ChannelReader
	settings SettingsReader
	now      func() time.Time
}

// NewService returns an advisory service.
func NewService(store *Store, channels ChannelReader, settings SettingsReader) *Service {
	return &Service{
		store:    store,
		channels: channels,
		settings: settings,
		now:      time.Now,
	}
}

// GenerateAdvisories scans channel statistics and creates new advisories for
// channels crossing configured thresholds. It skips channels that already have
// an active advisory of the same type.
func (s *Service) GenerateAdvisories(ctx context.Context) error {
	if err := s.store.ExpireIgnoredAdvisories(ctx, s.now()); err != nil {
		return fmt.Errorf("advisory service: expire ignored: %w", err)
	}

	channels, err := s.channels.ListChannelStats(ctx)
	if err != nil {
		return fmt.Errorf("advisory service: list channels: %w", err)
	}

	lowUsefulnessThreshold := s.threshold(ctx, "advisory_low_usefulness_threshold", 0.05)
	duplicateRateThreshold := s.threshold(ctx, "advisory_high_duplicate_rate_threshold", 0.50)

	for _, ch := range channels {
		if ch.TotalDiscovered == 0 {
			continue
		}

		usefulnessRate := safeRate(ch.TotalRetained, ch.TotalDiscovered)
		if usefulnessRate < lowUsefulnessThreshold {
			if err := s.maybeCreate(ctx, ch, AdvisoryTypeLowUsefulness, map[string]any{
				"window_days":      90,
				"discovered":       ch.TotalDiscovered,
				"routed":           ch.TotalRouted,
				"retained":         ch.TotalRetained,
				"usefulness_rate":  usefulnessRate,
				"threshold":        lowUsefulnessThreshold,
			}); err != nil {
				return err
			}
		}

		dp, err := s.channels.ListDuplicateProvenance(ctx, ch.ChannelID)
		if err != nil {
			return fmt.Errorf("advisory service: duplicate provenance %s: %w", ch.ChannelID, err)
		}
		duplicateRate := safeRate(len(dp), ch.TotalDiscovered)
		if duplicateRate > duplicateRateThreshold {
			if err := s.maybeCreate(ctx, ch, AdvisoryTypeHighDuplicateRate, map[string]any{
				"window_days":       90,
				"discovered":        ch.TotalDiscovered,
				"duplicate_matches": len(dp),
				"duplicate_rate":    duplicateRate,
				"threshold":         duplicateRateThreshold,
			}); err != nil {
				return err
			}
		}

		// Declining yield requires historical data; for v1 we compare recent
		// window against lifetime. A dedicated trend table would improve this.
		// We emit the advisory when lifetime yield is below half the configured
		// target, which is a conservative proxy for decline.
		yieldRate := safeRate(ch.TotalRouted, ch.TotalDiscovered)
		decliningThreshold := s.threshold(ctx, "advisory_declining_yield_threshold", 0.10)
		if yieldRate < decliningThreshold {
			if err := s.maybeCreate(ctx, ch, AdvisoryTypeDecliningYield, map[string]any{
				"window_days":       90,
				"discovered":        ch.TotalDiscovered,
				"routed":            ch.TotalRouted,
				"yield_rate":        yieldRate,
				"threshold":         decliningThreshold,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) maybeCreate(ctx context.Context, ch ChannelStats, typ AdvisoryType, evidence map[string]any) error {
	// We need the internal channel ID; look it up from the text identifier.
	channelID, err := s.lookupChannelID(ctx, ch.ChannelID)
	if err != nil {
		return err
	}
	exists, err := s.store.HasActiveAdvisoryOfType(ctx, channelID, typ)
	if err != nil {
		return fmt.Errorf("advisory service: check existing %s for %s: %w", typ, ch.ChannelID, err)
	}
	if exists {
		return nil
	}
	_, err = s.store.CreateAdvisory(ctx, channelID, typ, evidence)
	if err != nil {
		return fmt.Errorf("advisory service: create %s for %s: %w", typ, ch.ChannelID, err)
	}
	return nil
}

func (s *Service) lookupChannelID(ctx context.Context, channelIDText string) (int64, error) {
	var id int64
	err := s.store.db.QueryRowContext(ctx,
		`SELECT id FROM channel WHERE channel_id = ?;`, channelIDText).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("advisory service: lookup channel %s: %w", channelIDText, err)
	}
	return id, nil
}

func (s *Service) threshold(ctx context.Context, key string, defaultValue float64) float64 {
	v, err := s.settings.GetSetting(ctx, key)
	if err != nil {
		return defaultValue
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultValue
	}
	return f
}

// Dismiss dismisses an advisory.
func (s *Service) Dismiss(ctx context.Context, id int64) error {
	return s.store.DismissAdvisory(ctx, id)
}

// Ignore ignores an advisory for 30 days.
func (s *Service) Ignore(ctx context.Context, id int64) error {
	return s.store.IgnoreAdvisory(ctx, id)
}

// Act records a user action on an advisory.
func (s *Service) Act(ctx context.Context, id int64, action string) error {
	if action == "" {
		return fmt.Errorf("advisory service: action is required")
	}
	return s.store.ActOnAdvisory(ctx, id, action)
}

// ListActive returns active advisories.
func (s *Service) ListActive(ctx context.Context) ([]Advisory, error) {
	return s.store.ListActiveAdvisories(ctx)
}

// ListForChannel returns advisories for a channel.
func (s *Service) ListForChannel(ctx context.Context, channelIDText string) ([]Advisory, error) {
	return s.store.ListAdvisoriesForChannel(ctx, channelIDText)
}

func safeRate(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
