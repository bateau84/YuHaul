// Package pipeline orchestrates YuHaul's four-phase processing pipeline:
// discovery -> duplicate detection -> routing -> action execution.
//
// It manages the Run lifecycle, emits SSE events, enforces quota-aware action
// execution, and guarantees INV-1 terminal-state accounting.
package pipeline

import (
	"encoding/json"
	"sync"
	"time"
)

// Phase names the four pipeline phases.
type Phase string

const (
	PhaseDiscovery       Phase = "discovery"
	PhaseDuplicateDetect Phase = "duplicate_detection"
	PhaseRouting         Phase = "routing"
	PhaseActionExecution Phase = "action_execution"
)

// EventType names the SSE events emitted during a run.
type EventType string

const (
	EventRunStarted      EventType = "run.started"
	EventRunPhase        EventType = "run.phase"
	EventRunVideo        EventType = "run.video.processed"
	EventRunQuotaWarning EventType = "run.quota.warning"
	EventRunCompleted    EventType = "run.completed"
	EventRunError        EventType = "run.error"
)

// Event is a single SSE event for run progress.
type Event struct {
	Type      EventType       `json:"type"`
	RunID     int64           `json:"run_id"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// SSEPayload helpers.
type phasePayload struct {
	Phase Phase `json:"phase"`
}

type videoPayload struct {
	VideoID      int64  `json:"video_id"`
	VideoIDText  string `json:"video_id_text"`
	Outcome      string `json:"outcome"`
	MatchedRoute bool   `json:"matched_route"`
}

type quotaWarningPayload struct {
	Remaining int    `json:"remaining"`
	Message   string `json:"message"`
}

type completedPayload struct {
	RunID                 int64  `json:"run_id"`
	Status                string `json:"status"`
	VideosDiscovered      int    `json:"videos_discovered"`
	VideosAccepted        int    `json:"videos_accepted"`
	VideosRejected        int    `json:"videos_rejected"`
	VideosDeferred        int    `json:"videos_deferred"`
	VideosFailed          int    `json:"videos_failed"`
	VideosNotYetEvaluated int    `json:"videos_not_yet_evaluated"`
}

type errorPayload struct {
	Message string `json:"message"`
}

// Broadcaster distributes events to SSE subscribers for a run.
type Broadcaster struct {
	mu          sync.RWMutex
	subscribers map[int64]map[chan Event]struct{}
}

// NewBroadcaster returns an event broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subscribers: make(map[int64]map[chan Event]struct{})}
}

// Subscribe registers a new subscriber for runID. The returned channel receives
// events until Unsubscribe is called.
func (b *Broadcaster) Subscribe(runID int64) chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, 16)
	if b.subscribers[runID] == nil {
		b.subscribers[runID] = make(map[chan Event]struct{})
	}
	b.subscribers[runID][ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber.
func (b *Broadcaster) Unsubscribe(runID int64, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if subs, ok := b.subscribers[runID]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(b.subscribers, runID)
		}
	}
	close(ch)
}

// Broadcast sends an event to all subscribers of runID. It never blocks.
func (b *Broadcaster) Broadcast(runID int64, ev Event) {
	b.mu.RLock()
	subs := b.subscribers[runID]
	b.mu.RUnlock()
	for ch := range subs {
		select {
		case ch <- ev:
		default:
			// Subscriber is slow; drop event rather than block the pipeline.
		}
	}
}

func payload(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
