package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Machine orchestrates valid state transitions for the three-tier outcome model.
// It wraps a Store and enforces transition rules before persisting.
type Machine struct {
	store *Store
	now   func() time.Time
}

// NewMachine constructs a state machine backed by store.
func NewMachine(store *Store) *Machine {
	return &Machine{store: store, now: time.Now}
}

// transition describes an allowed processing-state change.
type transition struct {
	from VideoProcessingState
	to   VideoProcessingState
}

// allowedTransitions enumerates every valid processing-state edge. Any pair not
// listed is rejected, making illegal states unrepresentable at the API level.
var allowedTransitions = map[transition]struct{}{
	{VideoStateDiscovered, VideoStateNotYetEvaluated}: {},
	{VideoStateNotYetEvaluated, VideoStateEvaluating}: {},
	{VideoStateEvaluating, VideoStateEvaluated}:       {},
	{VideoStateEvaluating, VideoStateDeferred}:        {},
	{VideoStateEvaluating, VideoStateFailed}:          {},
	{VideoStateEvaluated, VideoStateActed}:            {},
	{VideoStateEvaluated, VideoStateDeferred}:         {},
	{VideoStateEvaluated, VideoStateFailed}:           {},
	{VideoStateActed, VideoStateCompleted}:            {},
	{VideoStateActed, VideoStateFailed}:               {},
	{VideoStateActed, VideoStateDeferred}:             {},
}

// ErrInvalidTransition is returned when a requested state change is not allowed.
var ErrInvalidTransition = errors.New("state: invalid processing-state transition")

// DiscoverVideo persists a newly discovered video in the not_yet_evaluated state.
// It is intended to be called during the discovery phase before any downstream
// processing begins.
func (m *Machine) DiscoverVideo(ctx context.Context, runID int64, videoID, channelID string) (*Video, error) {
	if videoID == "" {
		return nil, errors.New("state: videoID is required")
	}
	if channelID == "" {
		return nil, errors.New("state: channelID is required")
	}

	v := &Video{
		VideoID:           videoID,
		ChannelID:         channelID,
		DiscoveredAt:      m.now().UTC(),
		DiscoveredInRunID: runID,
		ProcessingState:   VideoStateNotYetEvaluated,
		FinalOutcome:      FinalOutcomeNotYetEvaluated,
	}
	id, err := m.store.CreateVideo(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("discovering video: %w", err)
	}
	v.ID = id
	return v, nil
}

// TransitionVideoState atomically moves a video's processing state from one
// known state to another. It validates the transition and maps the processing
// state to a final outcome where appropriate.
func (m *Machine) TransitionVideoState(ctx context.Context, videoID int64,
	from, to VideoProcessingState, opts ...TransitionOption) (*Video, error) {

	if !from.Valid() {
		return nil, fmt.Errorf("%w: from %q", ErrInvalidTransition, from)
	}
	if !to.Valid() {
		return nil, fmt.Errorf("%w: to %q", ErrInvalidTransition, to)
	}
	if _, ok := allowedTransitions[transition{from, to}]; !ok {
		return nil, fmt.Errorf("%w: %q -> %q", ErrInvalidTransition, from, to)
	}

	v, err := m.store.GetVideo(ctx, videoID)
	if err != nil {
		return nil, err
	}
	if v.ProcessingState != from {
		return nil, fmt.Errorf("%w: video is %q, expected %q", ErrInvalidTransition, v.ProcessingState, from)
	}

	cfg := transitionConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	outcome := processingStateToFinalOutcome(to, cfg.outcome)
	if cfg.outcome != "" {
		outcome = cfg.outcome
	}

	if err := m.store.UpdateVideoState(ctx, videoID, to, outcome, cfg.deferredReason, cfg.errorMessage); err != nil {
		return nil, fmt.Errorf("transitioning video %d: %w", videoID, err)
	}

	return m.store.GetVideo(ctx, videoID)
}

// processingStateToFinalOutcome maps terminal processing states to their default
// final outcomes. Non-terminal states keep the current final outcome unless an
// override is supplied.
func processingStateToFinalOutcome(state VideoProcessingState, override VideoFinalOutcome) VideoFinalOutcome {
	switch state {
	case VideoStateCompleted:
		return FinalOutcomeAccepted
	case VideoStateDeferred:
		if override != "" {
			return override
		}
		return FinalOutcomeDeferred
	case VideoStateFailed:
		return FinalOutcomeFailed
	default:
		if override != "" {
			return override
		}
		return FinalOutcomeNotYetEvaluated
	}
}

// transitionConfig carries optional overrides for a state transition.
type transitionConfig struct {
	outcome        VideoFinalOutcome
	deferredReason *string
	errorMessage   *string
}

// TransitionOption configures a state transition.
type TransitionOption func(*transitionConfig)

// WithFinalOutcome overrides the default final outcome for the transition.
func WithFinalOutcome(o VideoFinalOutcome) TransitionOption {
	return func(c *transitionConfig) {
		c.outcome = o
	}
}

// WithDeferredReason records why a video was deferred.
func WithDeferredReason(reason string) TransitionOption {
	return func(c *transitionConfig) {
		c.deferredReason = &reason
	}
}

// WithErrorMessage records an error message associated with the transition.
func WithErrorMessage(msg string) TransitionOption {
	return func(c *transitionConfig) {
		c.errorMessage = &msg
	}
}

// RecordRouteDecision persists a per-route decision and returns it.
func (m *Machine) RecordRouteDecision(ctx context.Context, d *RouteDecision) (*RouteDecision, error) {
	if d.VideoID == 0 {
		return nil, errors.New("state: VideoID is required")
	}
	if d.RunID == 0 {
		return nil, errors.New("state: RunID is required")
	}
	if d.RouteID == 0 {
		return nil, errors.New("state: RouteID is required")
	}
	if !d.Decision.Valid() {
		return nil, fmt.Errorf("state: invalid decision %q", d.Decision)
	}
	if d.Decision == RouteDecisionSkipped && d.SkipReason == nil {
		return nil, errors.New("state: skipped decision requires a SkipReason")
	}

	id, err := m.store.CreateRouteDecision(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("recording route decision: %w", err)
	}
	d.ID = id
	return d, nil
}

// RecordExecutedAction persists a per-action outcome.
func (m *Machine) RecordExecutedAction(ctx context.Context, a *ExecutedAction) (*ExecutedAction, error) {
	if a.RouteDecisionID == 0 {
		return nil, errors.New("state: RouteDecisionID is required")
	}
	if a.RunID == 0 {
		return nil, errors.New("state: RunID is required")
	}
	if a.VideoID == 0 {
		return nil, errors.New("state: VideoID is required")
	}
	if a.ActionType == "" {
		return nil, errors.New("state: ActionType is required")
	}
	if a.TargetPlaylistID == "" {
		return nil, errors.New("state: TargetPlaylistID is required")
	}
	if !a.Outcome.Valid() {
		return nil, fmt.Errorf("state: invalid action outcome %q", a.Outcome)
	}

	now := m.now().UTC()
	a.ExecutedAt = &now

	id, err := m.store.CreateExecutedAction(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("recording executed action: %w", err)
	}
	a.ID = id
	return a, nil
}

// RecordConditionAudit persists a condition audit record.
func (m *Machine) RecordConditionAudit(ctx context.Context, a *ConditionAudit) (*ConditionAudit, error) {
	if a.RouteDecisionID == 0 {
		return nil, errors.New("state: RouteDecisionID is required")
	}
	if a.Field == "" {
		return nil, errors.New("state: Field is required")
	}
	if a.Operator == "" {
		return nil, errors.New("state: Operator is required")
	}

	id, err := m.store.CreateConditionAudit(ctx, a)
	if err != nil {
		return nil, fmt.Errorf("recording condition audit: %w", err)
	}
	a.ID = id
	return a, nil
}

// CompleteRun finalizes a run and recomputes its outcome counters.
func (m *Machine) CompleteRun(ctx context.Context, runID int64) (*Run, error) {
	started := m.now().UTC()
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	duration := started.Sub(run.StartedAt).Seconds()
	if err := m.store.CompleteRun(ctx, runID, duration); err != nil {
		return nil, err
	}
	if err := m.store.RecomputeRunCounters(ctx, runID); err != nil {
		return nil, err
	}
	return m.store.GetRun(ctx, runID)
}

// CompleteHistoricalRun finalizes a replay/backtest run and recomputes its
// counters from route decisions rather than discovered_in_run_id.
func (m *Machine) CompleteHistoricalRun(ctx context.Context, runID int64) (*Run, error) {
	started := m.now().UTC()
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}

	duration := started.Sub(run.StartedAt).Seconds()
	if err := m.store.CompleteRun(ctx, runID, duration); err != nil {
		return nil, err
	}
	if err := m.store.RecomputeRunCountersFromDecisions(ctx, runID); err != nil {
		return nil, err
	}
	return m.store.GetRun(ctx, runID)
}

// txer is satisfied by *sql.DB and *sql.Tx so we can share scanning helpers.
type txer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// compile-time interface checks.
var (
	_ txer = (*sql.DB)(nil)
	_ txer = (*sql.Tx)(nil)
)
