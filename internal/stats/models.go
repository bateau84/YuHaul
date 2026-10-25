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
// RouteID is the route's stable public UUID (ARS-004); the internal integer
// route key never reaches the wire. RouteName is the label recorded in the
// run's immutable ConfigVersion snapshot (ARS-001).
//
// EvaluationOrder is the route's order in the requested run, not its current
// row order. It is projected from historical evidence only: the run's
// immutable ConfigVersion snapshot first, then persisted route_decision
// orders when the snapshot has no usable order for the route. It is omitted
// when neither source supplies one consistent positive order. The current
// route row's evaluation_order is never used: ADR-003/ADR-012 store a
// negative -route_id sentinel there for soft-deleted routes and proposal-only
// anchors (ARS-001).
type RouteStats struct {
	RouteID           string         `json:"route_id"`
	RouteName         string         `json:"route_name"`
	EvaluationOrder   *int           `json:"evaluation_order,omitempty"`
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
	ChannelID        string            `json:"channel_id"`
	ChannelName      string            `json:"channel_name"`
	TotalDiscovered  int               `json:"total_discovered"`
	TotalRouted      int               `json:"total_routed"`
	TotalRetained    int               `json:"total_retained"`
	Yield            float64           `json:"yield"`
	UsefulnessRate   float64           `json:"usefulness_rate"`
	DuplicateRate    float64           `json:"duplicate_rate"`
	LastDiscoveredAt *time.Time        `json:"last_discovered_at,omitempty"`
	Advisories       []AdvisorySummary `json:"advisories,omitempty"`
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

// DuplicateProvenance describes one distinct-pair duplicate match anchored at
// an evaluated video. DiscoveryCreditChannelID/Name name the earlier native
// observation of the matched pair (ADR-014 chronology) and are empty when the
// pair has no orderable native evidence; ChannelID is the evaluated video's
// channel, never an imported fingerprint association.
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

// RouteMatchRate aggregates route decisions for a single route and bucket.
// RouteID is the route's stable public UUID; RouteName is the current
// persisted display label (cross-run analytics are not run-scoped).
type RouteMatchRate struct {
	RouteID   string  `json:"route_id"`
	RouteName string  `json:"route_name"`
	Bucket    string  `json:"bucket"`
	Evaluated int     `json:"evaluated"`
	Matched   int     `json:"matched"`
	MatchRate float64 `json:"match_rate"`
}

// DuplicateProvenanceBreakdown counts how often a channel earned discovery
// credit as the earlier native observation of a distinct matched pair versus
// relevance credit as the later one (ADR-014). Pairs without orderable
// native chronology contribute to neither count.
type DuplicateProvenanceBreakdown struct {
	DiscoveryCredit int `json:"discovery_credit"`
	RelevanceCredit int `json:"relevance_credit"`
}

// RetentionProvenanceBreakdown counts retention signals by source.
type RetentionProvenanceBreakdown struct {
	UserDriven   int `json:"user_driven"`
	YuhaulRouted int `json:"yuhaul_routed"`
}

// MatchedRoute is one route identity's production match evidence for a
// channel: the stable public UUID, the current display label, and the number
// of distinct channel videos that route matched in production runs. Name is a
// label, never a key — two entries may carry the same name (ARS-001).
type MatchedRoute struct {
	RouteID    string `json:"route_id"`
	Name       string `json:"name"`
	MatchCount int    `json:"match_count"`
}

// ChannelDetail enriches ChannelStats with provenance, recent videos, matched
// routes, and advisories for the channel analysis view.
type ChannelDetail struct {
	ChannelStats
	DuplicateProvenance DuplicateProvenanceBreakdown `json:"duplicate_provenance"`
	RetentionSignals    RetentionProvenanceBreakdown `json:"retention_signals"`
	RecentVideos        []VideoSummary               `json:"recent_videos"`
	// RecentVideoProvenance is the depth-0 JSON key for per-video provenance;
	// it is matched client-side against recent_videos by video_id.
	RecentVideoProvenance []VideoProvenance `json:"recent_video_provenance"`
	// MatchedRoutes holds one entry per route identity with production match
	// evidence for this channel; same-named routes stay separate (ARS-001).
	MatchedRoutes []MatchedRoute `json:"matched_routes"`
	// Advisories shadows the embedded ChannelStats.Advisories at JSON depth 0.
	// Both serialize under "advisories" (the client contract); only this one is
	// populated on the detail path.
	Advisories []AdvisorySummary `json:"advisories"`
}

// VideoSummary is the subset of video state exposed on the channel detail view.
type VideoSummary struct {
	ID              int64      `json:"id"`
	VideoID         string     `json:"video_id"`
	ChannelID       string     `json:"channel_id"`
	Title           string     `json:"title,omitempty"`
	ProcessingState string     `json:"processing_state"`
	FinalOutcome    string     `json:"final_outcome"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	DiscoveredAt    time.Time  `json:"discovered_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

// VideoProvenance tags a recent video with duplicate/retention provenance.
// DuplicateCredit is "discovery" when the video was the earlier native
// observation of a distinct matched pair and "relevance" when it was the
// later one (ADR-014); it is absent when the video holds no chronology-based
// credit.
type VideoProvenance struct {
	VideoID         string  `json:"video_id"`
	DuplicateCredit *string `json:"duplicate_credit,omitempty"`
	RetentionSource *string `json:"retention_source,omitempty"`
}

// AdvisorySummary is the subset of an advisory shown on the channel view. Its
// JSON vocabulary matches the web client's Advisory type.
type AdvisorySummary struct {
	ID               int64          `json:"id"`
	ChannelID        string         `json:"channel_id"`
	ChannelName      string         `json:"channel_name"`
	AdvisoryType     string         `json:"advisory_type"`
	Status           string         `json:"status"`
	GeneratedAt      time.Time      `json:"generated_at"`
	IgnoredUntil     *time.Time     `json:"ignored_until,omitempty"`
	ActionTaken      *string        `json:"action_taken,omitempty"`
	EvidenceSnapshot map[string]any `json:"evidence_snapshot"`
}
