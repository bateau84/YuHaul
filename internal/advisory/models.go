// Package advisory implements YuHaul's evidence-backed advisory system.
//
// Advisories are generated from channel statistics. The system never acts
// autonomously; it only records user decisions (dismiss, ignore, act).
package advisory

import "time"

// AdvisoryType classifies the advisory.
type AdvisoryType string

const (
	AdvisoryTypeLowUsefulness     AdvisoryType = "low_usefulness"
	AdvisoryTypeDecliningYield    AdvisoryType = "declining_yield"
	AdvisoryTypeHighDuplicateRate AdvisoryType = "high_duplicate_rate"
)

// AdvisoryStatus tracks the user's disposition of an advisory.
type AdvisoryStatus string

const (
	AdvisoryStatusActive    AdvisoryStatus = "active"
	AdvisoryStatusDismissed AdvisoryStatus = "dismissed"
	AdvisoryStatusIgnored   AdvisoryStatus = "ignored"
	AdvisoryStatusActedUpon AdvisoryStatus = "acted_upon"
)

// Advisory is a recommendation generated from channel statistics.
type Advisory struct {
	ID               int64          `json:"id"`
	ChannelID        int64          `json:"channel_id"`
	ChannelIDText    string         `json:"channel_id_text"`
	ChannelName      string         `json:"channel_name"`
	Type             AdvisoryType   `json:"type"`
	Status           AdvisoryStatus `json:"status"`
	GeneratedAt      time.Time      `json:"generated_at"`
	IgnoredUntil     *time.Time     `json:"ignored_until,omitempty"`
	ActionTaken      *string        `json:"action_taken,omitempty"`
	DismissedAt      *time.Time     `json:"dismissed_at,omitempty"`
	ActedUponAt      *time.Time     `json:"acted_upon_at,omitempty"`
	EvidenceSnapshot map[string]any `json:"evidence_snapshot"`
}

// EvidenceSnapshot captures the metrics that triggered an advisory.
type EvidenceSnapshot struct {
	WindowDays        int      `json:"window_days"`
	Discovered        int      `json:"discovered"`
	Routed            int      `json:"routed"`
	Retained          int      `json:"retained"`
	DuplicateMatches  int      `json:"duplicate_matches"`
	Threshold         float64  `json:"threshold"`
	BaselineYieldRate *float64 `json:"baseline_yield_rate,omitempty"`
	CurrentYieldRate  *float64 `json:"current_yield_rate,omitempty"`
}
