// Package state implements YuHaul's three-tier outcome model state machine.
//
// The tiers are:
//  1. Video processing state — overall lifecycle position of a video within a run.
//  2. RouteDecision — per-route outcome for a video.
//  3. ExecutedAction — per-action outcome for a route decision.
package state

import (
	"encoding/json"
	"time"

	"github.com/bateau84/YuHaul/internal/diag"
)

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
	// RunStatusStopped is a run the user stopped (run-initiation ADR-004). It
	// is final and always carries StopRequestedAt.
	RunStatusStopped RunStatus = "stopped"
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
	// VideosSelected is the input count captured when the run's selector
	// established its input set. NULL means selection was never captured
	// (unknown, including all pre-migration runs); 0 means selection
	// completed and found no inputs (ARD-001).
	VideosSelected *int `json:"videos_selected"`
	// VideosEvaluated is the number of distinct videos with persisted
	// route-decision evidence for this run, derived at read time. It is not
	// a stored counter and does not change videos_discovered (ARD-001).
	VideosEvaluated int      `json:"videos_evaluated"`
	QuotaCost       int      `json:"quota_cost"`
	DurationSeconds *float64 `json:"duration_seconds"`
	// InterruptReason is the internal run recovery reason. Its public JSON
	// projection vettes the stored text (see MarshalJSON): unsafe legacy free
	// text is withheld and flagged unavailable instead of serialized raw
	// (ARD-001 failure diagnostic boundary).
	InterruptReason *string `json:"interrupt_reason"`
	CurrentPhase    *string `json:"current_phase"`
	// StopRequestedAt is the durable stop marker. It is set once, on a running
	// run, when the user presses Stop; the run then ends only as stopped.
	StopRequestedAt *time.Time `json:"stop_requested_at"`

	// ActionAuthorityVersion is write-once producer evidence; zero is legacy/unknown.
	ActionAuthorityVersion int `json:"-"`

	// InitiatedBy records who started a production run (InitiatedBy* values).
	// It is written once at insert; nil for every pre-024 row and for
	// simulation runs. Serialized by MarshalJSON as initiated_by.
	InitiatedBy *string `json:"-"`
	// InterruptCause records why an interrupted run stopped (InterruptCause*
	// values). Nil when the cause is unproven. Serialized by MarshalJSON as
	// interrupt_cause.
	InterruptCause *string `json:"-"`

	// PublishedFrom/PublishedTo are the Backtest/Replay published-date range
	// (UTC calendar days), written once at insert; nil means unbounded. They
	// reach clients through the published_range projection in MarshalJSON.
	PublishedFrom *string `json:"-"`
	PublishedTo   *string `json:"-"`
}

// Closed values of Run.InitiatedBy and Run.InterruptCause. They are enforced
// by the migration 024 triggers; new values need a new migration.
const (
	InitiatedByRunNow     = "run_now"
	InitiatedBySchedule   = "schedule"
	InitiatedByQueueRetry = "queue_retry"

	InterruptCauseTimeLimit = "time_limit"
	InterruptCauseShutdown  = "shutdown"
)

// MarshalJSON projects Run for public serialization. Admitted canonical and
// exact legacy reasons are normalized to v1. Rejected historical prose is
// withheld; generic unknown_error summaries retain safe phase/operation but
// set failure_reason_unavailable because the cause detail is not known
// (ARD-001).
func (r Run) MarshalJSON() ([]byte, error) {
	type alias Run
	out := struct {
		*alias
		InterruptReason          *string `json:"interrupt_reason"`
		FailureReasonUnavailable bool    `json:"failure_reason_unavailable,omitempty"`
		CurrentPhase             *string `json:"current_phase"`
		PublishedRange           any     `json:"published_range"`
		// InitiatedBy and InterruptCause are closed-set values (see the
		// constants above) that let Run Detail tell a time-limit-interrupted
		// Run Now run apart. Always present; null when unproven or legacy.
		InitiatedBy    *string `json:"initiated_by"`
		InterruptCause *string `json:"interrupt_cause"`
	}{alias: (*alias)(&r), InitiatedBy: r.InitiatedBy, InterruptCause: r.InterruptCause}
	if pr := r.publishedRange(); pr != nil {
		out.PublishedRange = pr
	}
	if r.InterruptReason != nil {
		if value, unavailable := diag.AdmitErrorText(*r.InterruptReason); value != "" {
			out.InterruptReason = &value
			out.FailureReasonUnavailable = unavailable
		} else {
			out.FailureReasonUnavailable = unavailable
		}
	}
	if r.CurrentPhase != nil {
		if phase, ok := diag.AdmitPhase(*r.CurrentPhase); ok {
			out.CurrentPhase = &phase
		}
	}
	return json.Marshal(out)
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
	FinalOutcomeKnown bool
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
	// ExclusionIndex identifies the zero-based occurrence in the run's immutable
	// configuration snapshot. Nil means this decision has no captured reference.
	ExclusionIndex *int
	// ExclusionSemanticsVersion identifies the interpretation of that occurrence.
	ExclusionSemanticsVersion *int
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
	// OutcomeReason explains an existing Outcome without adding a new
	// outcome category (for example already_completed for an idempotent
	// no-attempt). It is NULL when no reason was captured.
	OutcomeReason *string
	ErrorMessage  *string
	ExecutedAt    *time.Time
}

// UnresolvedMutation is a run-owned projection of a persisted playlist send
// guard. Its presence means application is unknown; it is not an attempt.
type UnresolvedMutation struct {
	ActionType       string
	TargetPlaylistID string
	PreparedAt       time.Time
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
