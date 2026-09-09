package stats

import (
	"context"

	"github.com/bateau84/YuHaul/internal/advisory"
)

// AdvisoryChannelReader returns an advisory.ChannelReader backed by this
// service. It adapts the stats-package models to the narrower advisory-package
// interface.
func (s *Service) AdvisoryChannelReader() advisory.ChannelReader {
	return &advisoryAdapter{svc: s}
}

type advisoryAdapter struct {
	svc *Service
}

func (a *advisoryAdapter) ListChannelStats(ctx context.Context) ([]advisory.ChannelStats, error) {
	list, err := a.svc.ChannelStats(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]advisory.ChannelStats, len(list))
	for i, cs := range list {
		out[i] = advisory.ChannelStats{
			ChannelID:        cs.ChannelID,
			ChannelName:      cs.ChannelName,
			TotalDiscovered:  cs.TotalDiscovered,
			TotalRouted:      cs.TotalRouted,
			TotalRetained:    cs.TotalRetained,
			LastDiscoveredAt: cs.LastDiscoveredAt,
		}
	}
	return out, nil
}

func (a *advisoryAdapter) ListDuplicateProvenance(ctx context.Context, channelIDText string) ([]advisory.DuplicateProvenance, error) {
	list, err := a.svc.DuplicateProvenance(ctx, channelIDText)
	if err != nil {
		return nil, err
	}
	out := make([]advisory.DuplicateProvenance, len(list))
	for i, dp := range list {
		out[i] = advisory.DuplicateProvenance{VideoID: dp.VideoID}
	}
	return out, nil
}
