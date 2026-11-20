package quota

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// AdmitScoped (authorization ARS-001, final POST admission): the transition
// happens inside the caller's scope, after the time, bucket and context are
// sampled again, and an error leaves the hold provisional.

func scopedFixture(t *testing.T) (*Ledger, *Hold, *atomic.Int64) {
	t.Helper()
	l, _ := newTestLedger(t)
	var clock atomic.Int64
	clock.Store(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC).UnixNano())
	l.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	h, err := l.Acquire(context.Background(), OpPlaylistItemsInsert)
	if err != nil || h == nil {
		t.Fatalf("acquire: %v %v", h, err)
	}
	return l, h, &clock
}

func TestAdmitScopedTransitionsInsideTheScopeWithResampledTime(t *testing.T) {
	_, h, clock := scopedFixture(t)
	before := h.AdmissionTime()
	var inside atomic.Bool
	err := h.AdmitScoped(context.Background(), func(ctx context.Context, transition func() error) error {
		inside.Store(true)
		clock.Add(int64(3 * time.Second)) // the local wait took three seconds
		if err := transition(); err != nil {
			return err
		}
		if h.state != 1 {
			t.Error("not admitted inside the scope")
		}
		return nil
	}, nil)
	if err != nil || !inside.Load() {
		t.Fatalf("admit: %v inside=%v", err, inside.Load())
	}
	if got := h.AdmissionTime(); !got.Equal(before.Add(3 * time.Second)) {
		t.Fatalf("admission time %v was not resampled after the wait (acquired %v)", got, before)
	}
	if err := h.Cancel(); err == nil {
		t.Fatal("an admitted hold must not be cancellable")
	}
}

func TestAdmitScopedRefusalsLeaveTheHoldProvisional(t *testing.T) {
	refusal := errors.New("fence refused")
	for _, tc := range []struct {
		name  string
		scope func(clock *atomic.Int64, cancel context.CancelFunc) AdmissionScope
		valid func(time.Time) error
		want  string
	}{
		{"scope refuses before the transition", func(*atomic.Int64, context.CancelFunc) AdmissionScope {
			return func(context.Context, func() error) error { return refusal }
		}, nil, "fence refused"},
		{"validator refuses at the resampled time", func(*atomic.Int64, context.CancelFunc) AdmissionScope {
			return func(_ context.Context, transition func() error) error { return transition() }
		}, func(time.Time) error { return refusal }, "fence refused"},
		{"context ends between validation and transition", func(_ *atomic.Int64, cancel context.CancelFunc) AdmissionScope {
			return func(_ context.Context, transition func() error) error { cancel(); return transition() }
		}, nil, "context canceled"},
		{"bucket changes during the wait", func(clock *atomic.Int64, _ context.CancelFunc) AdmissionScope {
			return func(_ context.Context, transition func() error) error {
				clock.Add(int64(24 * time.Hour))
				return transition()
			}
		}, nil, "bucket changed"},
		{"scope never transitions", func(*atomic.Int64, context.CancelFunc) AdmissionScope {
			return func(context.Context, func() error) error { return nil }
		}, nil, "did not admit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, h, clock := scopedFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err := h.AdmitScoped(ctx, tc.scope(clock, cancel), tc.valid)
			if err == nil || !contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if h.state != 0 {
				t.Fatalf("hold state %d, want provisional", h.state)
			}
			if err := h.Cancel(); err != nil {
				t.Fatalf("hold is not cancellable: %v", err)
			}
			if len(l.holds) != 0 {
				t.Fatal("cancelled hold leaked capacity")
			}
		})
	}
}

// A failure to release the scope after the transition does not retract it.
func TestAdmitScopedKeepsAnAdmissionWhenTheScopeFailsToRelease(t *testing.T) {
	_, h, _ := scopedFixture(t)
	err := h.AdmitScoped(context.Background(), func(_ context.Context, transition func() error) error {
		if err := transition(); err != nil {
			return err
		}
		return errors.New("commit of the no-op writer failed")
	}, nil)
	if err != nil || h.state != 1 {
		t.Fatalf("err=%v state=%d, want admitted", err, h.state)
	}
}

func TestAdmitScopedTakesTheLedgerMutexBeforeTheScope(t *testing.T) {
	l, h, _ := scopedFixture(t)
	ran := make(chan struct{})
	err := h.AdmitScoped(context.Background(), func(_ context.Context, transition func() error) error {
		// While the scope runs the ledger mutex is held: a concurrent ledger
		// operation waits, which is the auth, quota, writer order.
		go func() {
			l.mu.Lock()
			l.mu.Unlock()
			close(ran)
		}()
		select {
		case <-ran:
			t.Error("ledger mutex was not held inside the scope")
		case <-time.After(50 * time.Millisecond):
		}
		return transition()
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-ran
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
