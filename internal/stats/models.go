// Package stats implements YuHaul's analytics layer: run/route/channel
// statistics, temporal aggregation, duplicate provenance, and retention signals.
package stats

import "time"

// RunStats is a computed view of a single run's outcomes, rejection breakdown,
// and quota consumption.
type RunStats struct {
	RunID                 int64          `json:"run_id"`
	RunType               string         `json:"run_type"`
	Status                string         `json:"status"`
	StartedAt             time.Time      `json:"started_at"`
	CompletedAt           *time.Time     `json:"completed_at,omitempty"`
	ConfigVersionID       *int64         `json:"config_version_id,omitempty"`
	VideosDiscovered      int            `json:"videos_discovered"`
	VideosAccepted        int            `json:"videos_accepted"`
	VideosRejected        int            `json:"videos_rejected"`
	VideosDeferred        int            `json:"videos_deferred"`
	VideosFailed          int            `json:"videos_failed"`
	VideosNotYetEvaluated int            `json:"videos_not_yet_evaluated"`
	RejectionByReason     map[string]int `json:"rejection_by_reason"`
	QuotaByOperation      map[string]int `json:"quota_by_operation"`
	DurationSeconds       *float64       `json:"duration_seconds,omitempty"`
}

// RouteStats is a computed view of a route's performance within a run.
type RouteStats struct {
	RouteID           int64          `json:"route_id"`
	RouteName         string         `json:"route_name"`
	EvaluationOrder   int            `json:"evaluation_order"`
	VideosEvaluated   int            `json:"videos_evaluated"`
	VideosMatched     int            `json:"videos_matched"`
	VideosNotMatched  int            `json:"videos_not_matched"`
	VideosSkipped     int            `json:"videos_skipped"`
	RejectionByReason map[string]int `json:"rejection_by_reason"`
	SkipBreakdown     map[string]int `json:"skip_breakdown"`
	ActionsExecuted   int            `json:"actions_executed"`
	ActionsSucceeded  int            `json:"actions_succeeded"`
	ActionsFailed     int            `json:"actions_failed"`
}

// ChannelStats are materialized counters for a channel.
type ChannelStats struct {
	ChannelID        string     `json:"channel_id"`
	ChannelName      string     `json:"channel_name"`
	TotalDiscovered  int        `json:"total_discovered"`
	TotalRouted      int        `json:"total_routed"`
	TotalRetained    int        `json:"total_retained"`
	YieldRate        float64    `json:"yield_rate"`
	UsefulnessRate   float64    `json:"usefulness_rate"`
	DuplicateRate    float64    `json:"duplicate_rate"`
	LastDiscoveredAt *time.Time `json:"last_discovered_at,omitempty"`
}

// TemporalBucket aggregates run outcomes over a calendar period.
type TemporalBucket struct {
	Bucket                string `json:"bucket"`
	VideosDiscovered      int    `json:"videos_discovered"`
	VideosAccepted        int    `json:"videos_accepted"`
	VideosRejected        int    `json:"videos_rejected"`
	VideosDeferred        int    `json:"videos_deferred"`
	VideosFailed          int    `json:"videos_failed"`
	VideosNotYetEvaluated int    `json:"videos_not_yet_evaluated"`
}

// DuplicateProvenance breaks down which channel originally discovered a video
// versus which channel published a matching duplicate.
type DuplicateProvenance struct {
	VideoID                    string  `json:"video_id"`
	ChannelID                  string  `json:"channel_id"`
	MatchedFingerprintID       int64   `json:"matched_fingerprint_id"`
	SimilarityScore            float64 `json:"similarity_score"`
	MatchType                  string  `json:"match_type"`
	DiscoveryCreditChannelID   string  `json:"discovery_credit_channel_id"`
	DiscoveryCreditChannelName string  `json:"discovery_credit_channel_name"`
}

// RetentionPlaylist is a user-designated playlist used for usefulness signals.
type RetentionPlaylist struct {
	ID             int64      `json:"id"`
	PlaylistID     string     `json:"playlist_id"`
	PlaylistName   string     `json:"playlist_name"`
	DesignatedAt   time.Time  `json:"designated_at"`
	UnDesignatedAt *time.Time `json:"un_designated_at,omitempty"`
	IsActive       bool       `json:"is_active"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
}

// RetentionSignal records whether a video's playlist membership counts as a
// genuine retention signal, and why.
type RetentionSignal struct {
	ID                int64     `json:"id"`
	VideoID           string    `json:"video_id"`
	PlaylistID        string    `json:"playlist_id"`
	MembershipSource  string    `json:"membership_source"`
	IsRetentionSignal bool      `json:"is_retention_signal"`
	DetectedAt        time.Time `json:"detected_at"`
}

// QuotaBreakdown returns the ledger entries for a run or date range.
type QuotaBreakdown struct {
	Date      string `json:"date"`
	Operation string `json:"operation"`
	Units     int    `json:"units"`
	CallCount int    `json:"call_count"`
}
