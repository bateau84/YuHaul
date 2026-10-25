// Package state implements YuHaul's three-tier outcome model state machine.
//
// The tiers are:
//  1. Video processing state — overall lifecycle position of a video within a run.
//  2. RouteDecision — per-route outcome for a video.
//  3. ExecutedAction — per-action outcome for a route decision.
package state

import "time"

// VideoProcessingState is the overall lifecycle state of a video within a run.
type VideoProcessingState string

const (
	VideoStateDiscovered      VideoProcessingState = "discovered"
	VideoStateNotYetEvaluated VideoProcessingState = "not_yet_evaluated"
	VideoStateEvaluating      VideoProcessingState = "evaluating"
	VideoStateEvaluated       VideoProcessingState = "evaluated"
	VideoStateActed           VideoProcessingState = "acted"
	VideoStateCompleted       VideoProcessingState = "completed"
	VideoStateDeferred        VideoProcessingState = "deferred"
	VideoStateFailed          VideoProcessingState = "failed"
)

// VideoFinalOutcome is the terminal classification of a video after processing.
type VideoFinalOutcome string

const (
	FinalOutcomeAccepted        VideoFinalOutcome = "accepted"
	FinalOutcomeRejected        VideoFinalOutcome = "rejected"
	FinalOutcomeExcluded        VideoFinalOutcome = "excluded"
	FinalOutcomeDeferred        VideoFinalOutcome = "deferred"
	FinalOutcomeFailed          VideoFinalOutcome = "failed"
	FinalOutcomeNotYetEvaluated VideoFinalOutcome = "not_yet_evaluated"
)

// RouteDecisionType records the result of evaluating a route against a video.
type RouteDecisionType string

const (
	RouteDecisionMatched    RouteDecisionType = "matched"
	RouteDecisionNotMatched RouteDecisionType = "not_matched"
	RouteDecisionDeferred   RouteDecisionType = "deferred"
	RouteDecisionSkipped    RouteDecisionType = "skipped"
)

// SkipReason explains why a route decision was skipped.
type SkipReason string

const (
	SkipReasonFinalRouteMatched SkipReason = "final_route_matched"
	SkipReasonExclusion         SkipReason = "exclusion"
	SkipReasonDisabled          SkipReason = "disabled"
)

// ActionOutcome records the result of executing an action.
type ActionOutcome string

const (
	ActionOutcomeSucceeded    ActionOutcome = "succeeded"
	ActionOutcomeFailed       ActionOutcome = "failed"
	ActionOutcomeDeferred     ActionOutcome = "deferred"
	ActionOutcomeNotAttempted ActionOutcome = "not_attempted"
)

// RunType classifies the execution mode of a run.
type RunType string

const (
	RunTypeScheduled RunType = "scheduled"
	RunTypeManual    RunType = "manual"
	RunTypeDryRun    RunType = "dry_run"
	RunTypeReplay    RunType = "replay"
	RunTypeBacktest  RunType = "backtest"
)

// RunStatus records the execution status of a run.
type RunStatus string

const (
	RunStatusRunning     RunStatus = "running"
	RunStatusCompleted   RunStatus = "completed"
	RunStatusInterrupted RunStatus = "interrupted"
	RunStatusFailed      RunStatus = "failed"
)

// Run is a single execution of the processing pipeline.
type Run struct {
	ID                    int64      `json:"id"`
	StartedAt             time.Time  `json:"started_at"`
	CompletedAt           *time.Time `json:"completed_at"`
	RunType               RunType    `json:"run_type"`
	Status                RunStatus  `json:"status"`
	ConfigVersionID       *int64     `json:"config_version_id"`
	VideosDiscovered      int        `json:"videos_discovered"`
	VideosAccepted        int        `json:"videos_accepted"`
	VideosRejected        int        `json:"videos_rejected"`
	VideosExcluded        int        `json:"videos_excluded"`
	VideosDeferred        int        `json:"videos_deferred"`
	VideosFailed          int        `json:"videos_failed"`
	VideosNotYetEvaluated int        `json:"videos_not_yet_evaluated"`
	QuotaCost             int        `json:"quota_cost"`
	DurationSeconds       *float64   `json:"duration_seconds"`
	InterruptReason       *string    `json:"interrupt_reason"`
	CurrentPhase          *string    `json:"current_phase"`

	// ActionAuthorityVersion is write-once producer evidence; zero is legacy/unknown.
	ActionAuthorityVersion int `json:"-"`
}

// Video represents a discovered video and its processing state.
type Video struct {
	ID                int64
	VideoID           string
	ChannelID         string
	DiscoveredAt      time.Time
	DiscoveredInRunID int64
	ProcessingState   VideoProcessingState
	FinalOutcome      VideoFinalOutcome
	DeferredReason    *string
	ErrorMessage      *string
	CompletedAt       *time.Time
}

// RouteDecision records the outcome of evaluating one route against one video.
type RouteDecision struct {
	ID              int64
	VideoID         int64
	RunID           int64
	RouteID         int64
	EvaluationOrder int
	Decision        RouteDecisionType
	SkipReason      *SkipReason
	RejectionReason *string
}

// ExecutedAction records the outcome of executing one action.
type ExecutedAction struct {
	ID               int64
	RouteDecisionID  int64
	RunID            int64
	VideoID          int64
	ActionType       string
	TargetPlaylistID string
	Outcome          ActionOutcome
	ErrorMessage     *string
	ExecutedAt       *time.Time
}

// ConditionAudit records the evaluation of one condition within a route decision.
type ConditionAudit struct {
	ID              int64
	RouteDecisionID int64
	ConditionIndex  int
	Field           string
	Operator        string
	ExpectedValue   string
	ActualValue     *string
	Matched         bool
}

// Valid returns true if s is a known processing state.
func (s VideoProcessingState) Valid() bool {
	switch s {
	case VideoStateDiscovered, VideoStateNotYetEvaluated, VideoStateEvaluating,
		VideoStateEvaluated, VideoStateActed, VideoStateCompleted,
		VideoStateDeferred, VideoStateFailed:
		return true
	}
	return false
}

// Valid returns true if o is a known final outcome.
func (o VideoFinalOutcome) Valid() bool {
	switch o {
	case FinalOutcomeAccepted, FinalOutcomeRejected, FinalOutcomeExcluded,
		FinalOutcomeDeferred, FinalOutcomeFailed, FinalOutcomeNotYetEvaluated:
		return true
	}
	return false
}

// Valid returns true if d is a known route decision.
func (d RouteDecisionType) Valid() bool {
	switch d {
	case RouteDecisionMatched, RouteDecisionNotMatched, RouteDecisionDeferred, RouteDecisionSkipped:
		return true
	}
	return false
}

// Valid returns true if o is a known action outcome.
func (o ActionOutcome) Valid() bool {
	switch o {
	case ActionOutcomeSucceeded, ActionOutcomeFailed, ActionOutcomeDeferred, ActionOutcomeNotAttempted:
		return true
	}
	return false
}
