package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store/storetest"
)

// DispatchWithQuotaScoped (authorization ARS-001, final POST admission): the
// scope runs after auth and quota, is released before transport, and a fence
// refusal admits nothing, charges nothing and leaks no hold.

func scopedDispatchFixture(t *testing.T) (context.Context, *Service, AuthorizationBinding, *quota.Ledger, *http.Request) {
	t.Helper()
	ctx := context.Background()
	db := storetest.Open(t)
	ledger := quota.New(db)
	if err := ledger.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, store, time.Now().Add(time.Hour))
	s := authorityTestService(t, store, nil)
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, s, binding, ledger, req
}

func TestDispatchWithQuotaScopedOrderAndRelease(t *testing.T) {
	ctx, s, binding, ledger, req := scopedDispatchFixture(t)
	var scopeOpen, sawAuthLocked, sawLedgerLocked atomic.Bool
	var sends atomic.Int32
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) {
		sends.Add(1)
		if scopeOpen.Load() {
			t.Error("the local scope was still open during transport")
		}
		return authorityTestResponse("{}"), nil
	})
	scope := func(_ context.Context, transition func() error) error {
		scopeOpen.Store(true)
		defer scopeOpen.Store(false)
		// Held by the time the scope runs: auth fence and quota mutex.
		sawAuthLocked.Store(!s.mu.TryLock())
		// A concurrent ledger operation must wait for the quota mutex.
		probe := make(chan struct{})
		go func() { _, _ = ledger.Acquire(ctx, quota.OpVideosList); close(probe) }()
		select {
		case <-probe:
		case <-time.After(100 * time.Millisecond):
			sawLedgerLocked.Store(true)
		}
		return transition()
	}
	resp, receipt, hold, err := s.DispatchWithQuotaScoped(ctx, binding, req, transport, ledger, quota.OpVideosList, scope)
	if err != nil || hold == nil || receipt.AdmittedAt.IsZero() || sends.Load() != 1 {
		t.Fatalf("dispatch: hold=%v receipt=%+v err=%v sends=%d", hold, receipt, err, sends.Load())
	}
	resp.Body.Close()
	if !sawAuthLocked.Load() || !sawLedgerLocked.Load() {
		t.Fatalf("scope ran outside the auth/quota order: auth=%v quota=%v", sawAuthLocked.Load(), sawLedgerLocked.Load())
	}
	if err := hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchWithQuotaScopedFenceRefusalAdmitsAndChargesNothing(t *testing.T) {
	ctx, s, binding, ledger, req := scopedDispatchFixture(t)
	cause := errors.New("receipt lost")
	var sends atomic.Int32
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) { sends.Add(1); return authorityTestResponse("{}"), nil })
	_, receipt, hold, err := s.DispatchWithQuotaScoped(ctx, binding, req, transport, ledger, quota.OpVideosList,
		func(context.Context, func() error) error { return &AdmissionFenceError{Cause: cause} })
	var fence *AdmissionFenceError
	if !errors.As(err, &fence) || !errors.Is(err, cause) || errors.Is(err, ErrQuotaAccounting) || hold != nil || !receipt.AdmittedAt.IsZero() || sends.Load() != 0 {
		t.Fatalf("refusal: err=%v hold=%v receipt=%+v sends=%d", err, hold, receipt, sends.Load())
	}
	// The provisional hold was cancelled: the single unit is still available,
	// and the failed dispatch did not hold the authority fence.
	h, err := ledger.Acquire(ctx, quota.OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("hold leaked: %v %v", h, err)
	}
	if err := h.Cancel(); err != nil {
		t.Fatal(err)
	}
	if !s.mu.TryLock() {
		t.Fatal("authority fence still held after refusal")
	}
	s.mu.Unlock()
	// A later dispatch with a passing scope is admitted: the refusal changed no state.
	resp, _, hold, err := s.DispatchWithQuotaScoped(ctx, binding, req, transport, ledger, quota.OpVideosList,
		func(_ context.Context, transition func() error) error { return transition() })
	if err != nil || hold == nil || sends.Load() != 1 {
		t.Fatalf("later dispatch: hold=%v err=%v sends=%d", hold, err, sends.Load())
	}
	resp.Body.Close()
	if err := hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchWithQuotaScopedRequiresALedgerAndScope(t *testing.T) {
	ctx, s, binding, ledger, req := scopedDispatchFixture(t)
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) { t.Error("sent"); return authorityTestResponse("{}"), nil })
	if _, _, _, err := s.DispatchWithQuotaScoped(ctx, binding, req, transport, ledger, quota.OpVideosList, nil); !errors.Is(err, ErrQuotaAccounting) {
		t.Fatalf("nil scope: %v", err)
	}
	if _, _, _, err := s.DispatchWithQuotaScoped(ctx, binding, req, transport, nil, quota.OpVideosList, func(context.Context, func() error) error { return nil }); !errors.Is(err, ErrQuotaAccounting) {
		t.Fatalf("nil ledger: %v", err)
	}
}

// A credential save that fails right after a successful renewal is a
// pre-admission refusal: Dispatch resolves before it builds the request,
// takes a hold or enters transport, so nothing was sent, held or charged. The
// failure heals once the store recovers, and then the request goes out once.
func TestDispatchRenewalPersistenceFailureIsPreAdmissionRefusal(t *testing.T) {
	ctx := context.Background()
	key := newTestKey(t)
	store := &failingStore{SQLiteTokenStore: newTestStore(t, key)}
	seedExpired(t, store.SQLiteTokenStore)
	store.failRefresh.Store(true)
	store.failRenewalFailure.Store(true)
	var exchanges atomic.Int32
	svc := newExpiredRotatingService(t, store, key, &exchanges, nil)
	binding, err := svc.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ledger := quota.New(storetest.Open(t))
	if err := ledger.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	transport := authorityTestTransport(func(r *http.Request) (*http.Response, error) {
		sends.Add(1)
		return authorityTestResponse("{}"), nil
	})

	resp, receipt, hold, err := svc.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, ErrRenewalPersistence) || !IsPreAdmissionAuthorizationRefusal(err) {
		t.Fatalf("err = %v, want a pre-admission refusal for the persistence failure", err)
	}
	if hold != nil || !receipt.AdmittedAt.IsZero() || receipt.AuthorizationID != "" || sends.Load() != 0 {
		t.Fatalf("something was admitted: hold=%v receipt=%+v sends=%d", hold, receipt, sends.Load())
	}
	// No hold was ever taken: the single unit is free.
	h, err := ledger.Acquire(ctx, quota.OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("a hold leaked: %v %v", h, err)
	}
	if err := h.Cancel(); err != nil {
		t.Fatal(err)
	}
	// The same classification applies to the scoped dispatch the executor uses.
	_, receipt, hold, err = svc.DispatchWithQuotaScoped(ctx, binding, req, transport, ledger, quota.OpVideosList,
		func(_ context.Context, transition func() error) error {
			t.Error("scope entered before admission")
			return transition()
		})
	if !IsPreAdmissionAuthorizationRefusal(err) || hold != nil || receipt.AuthorizationID != "" || sends.Load() != 0 {
		t.Fatalf("scoped: err=%v hold=%v receipt=%+v sends=%d", err, hold, receipt, sends.Load())
	}
	// The classification stays narrow: cancellation and unrelated errors are not refusals.
	if IsPreAdmissionAuthorizationRefusal(context.Canceled) || IsPreAdmissionAuthorizationRefusal(errors.New("x")) || IsPreAdmissionAuthorizationRefusal(errClientConfiguration) {
		t.Fatal("classification widened")
	}

	// Recovery: the retained candidate saves, and the next dispatch sends once.
	store.failRefresh.Store(false)
	store.failRenewalFailure.Store(false)
	resp, _, hold, err = svc.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
	if err != nil || hold == nil || sends.Load() != 1 {
		t.Fatalf("recovered dispatch: hold=%v err=%v sends=%d", hold, err, sends.Load())
	}
	resp.Body.Close()
	if err := hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
}
