package auth

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/store/storetest"
	"modernc.org/sqlite"
)

type scheduleBarrierConnector struct {
	drv              driver.Driver
	reads            atomic.Int32
	entered, release chan struct{}
}

func (c *scheduleBarrierConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.drv.Open(":memory:?_fk=1")
	if err != nil {
		return nil, err
	}
	return &scheduleBarrierConn{Conn: conn, barrier: c}, nil
}
func (c *scheduleBarrierConnector) Driver() driver.Driver { return c.drv }

type scheduleBarrierConn struct {
	driver.Conn
	barrier *scheduleBarrierConnector
}

func (c *scheduleBarrierConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "FROM quota_settings") && len(args) == 1 && args[0].Value == "reset_timezone" && c.barrier.reads.Add(1) == 2 {
		close(c.barrier.entered)
		select {
		case <-c.barrier.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestDispatchWithQuotaExpiryDuringAdmissionScheduleRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	barrier := &scheduleBarrierConnector{drv: &sqlite.Driver{}, entered: make(chan struct{}), release: make(chan struct{})}
	var release sync.Once
	db := sql.OpenDB(barrier)
	db.SetMaxOpenConns(1)
	defer db.Close()
	defer release.Do(func() { close(barrier.release) })
	if err := store.ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	ledger := quota.New(db)
	if err := ledger.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	tokenStore := newTestStore(t, newTestKey(t))
	authorityTestSeed(t, tokenStore, time.Now().Add(-time.Hour))
	var renewals, sends atomic.Int32
	s := authorityTestService(t, tokenStore, func(*http.Request) (*http.Response, error) {
		if renewals.Add(1) == 1 {
			return authorityTestResponse(`{"access_token":"renewed-short","token_type":"Bearer","expires_in":14}`), nil
		}
		return authorityTestResponse(`{"access_token":"renewed-valid","token_type":"Bearer","expires_in":3600}`), nil
	})
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) {
		sends.Add(1)
		return authorityTestResponse("{}"), nil
	})
	type result struct {
		receipt AdmissionReceipt
		hold    *quota.Hold
		err     error
	}
	done := make(chan result, 1)
	go func() {
		resp, receipt, hold, err := s.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
		if resp != nil {
			resp.Body.Close()
		}
		done <- result{receipt, hold, err}
	}()
	select {
	case <-barrier.entered:
	case r := <-done:
		t.Fatalf("dispatch returned before admission schedule read: %v", r.err)
	case <-ctx.Done():
		t.Fatal("admission schedule read did not block")
	}
	record, err := tokenStore.Load(ctx)
	if err != nil || record == nil {
		t.Fatalf("load renewed token: %v", err)
	}
	if renewals.Load() != 1 || record.AccessToken != "renewed-short" {
		t.Fatal("renewal was not durably committed before quota read")
	}
	boundary := record.Expiry.Add(-10 * time.Second)
	if !time.Now().Before(boundary) {
		t.Fatal("token reached expiry margin before admission schedule read blocked")
	}
	timer := time.NewTimer(time.Until(boundary) + 10*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	release.Do(func() { close(barrier.release) })
	r := <-done
	if !errors.Is(r.err, ErrAccessExpiredBeforeAdmission) || r.hold != nil || !r.receipt.AdmittedAt.IsZero() || sends.Load() != 0 {
		t.Fatalf("expired admission: err=%v hold=%v receipt=%+v sends=%d", r.err, r.hold, r.receipt, sends.Load())
	}
	resp, receipt, hold, err := s.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
	if err != nil || hold == nil || receipt.AdmittedAt.IsZero() || sends.Load() != 1 || renewals.Load() != 2 {
		t.Fatalf("subsequent dispatch: err=%v hold=%v receipt=%+v sends=%d renewals=%d", err, hold, receipt, sends.Load(), renewals.Load())
	}
	resp.Body.Close()
	if err := hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchWithQuotaAccountsOnlyAdmittedAttempt(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	ledger := quota.New(db)
	if err := ledger.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, newTestKey(t))
	store.db.SetMaxOpenConns(1)
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
	var sends atomic.Int32
	transport := authorityTestTransport(func(r *http.Request) (*http.Response, error) {
		sends.Add(1)
		if r.Header.Get("Authorization") != "Bearer old-access" {
			t.Error("incorrect authorization")
		}
		return authorityTestResponse("{}"), nil
	})
	resp, receipt, hold, err := s.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
	if err != nil || hold == nil || receipt.AdmittedAt.IsZero() {
		t.Fatalf("dispatch: %v, %v", hold, err)
	}
	resp.Body.Close()
	if err := hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	_, receipt, hold, err = s.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
	if !errors.Is(err, ErrQuotaRefused) || hold != nil || !receipt.AdmittedAt.IsZero() || sends.Load() != 1 {
		t.Fatalf("refusal: hold=%v receipt=%+v err=%v sends=%d", hold, receipt, err, sends.Load())
	}
}

// A blocked SQLite connection pauses the local quota reads while dispatch owns
// the authorization fence. Cancellation must release any provisional capacity.
func TestDispatchWithQuotaCancelledDuringQuotaRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := storetest.Open(t)
	ledger := quota.New(db)
	if err := ledger.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, newTestKey(t))
	store.db.SetMaxOpenConns(1)
	authorityTestSeed(t, store, time.Now().Add(time.Hour))
	s := authorityTestService(t, store, nil)
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waits := db.Stats().WaitCount
	callCtx, stop := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, "https://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	done := make(chan error, 1)
	go func() {
		_, receipt, hold, err := s.DispatchWithQuota(callCtx, binding, req, authorityTestTransport(func(*http.Request) (*http.Response, error) { sends.Add(1); return authorityTestResponse("{}"), nil }), ledger, quota.OpVideosList)
		if hold != nil || !receipt.AdmittedAt.IsZero() {
			done <- errors.New("cancelled dispatch admitted")
			return
		}
		done <- err
	}()
	for db.Stats().WaitCount == waits {
		select {
		case <-ctx.Done():
			t.Fatal("quota read did not block")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("dispatch: %v", err)
	}
	if sends.Load() != 0 {
		t.Fatal("cancelled dispatch sent request")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	h, err := ledger.Acquire(ctx, quota.OpVideosList)
	if err != nil || h == nil {
		t.Fatalf("capacity not available: %v", err)
	}
	if err := h.Cancel(); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchWithQuotaConcurrentLastUnit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := storetest.Open(t)
	ledger := quota.New(db)
	if err := ledger.SetDailyLimit(ctx, 1); err != nil {
		t.Fatal(err)
	}
	store := newTestStore(t, newTestKey(t))
	store.db.SetMaxOpenConns(1)
	authorityTestSeed(t, store, time.Now().Add(time.Hour))
	s := authorityTestService(t, store, nil)
	binding, err := s.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sends atomic.Int32
	transport := authorityTestTransport(func(*http.Request) (*http.Response, error) { sends.Add(1); return authorityTestResponse("{}"), nil })
	type result struct {
		response *http.Response
		receipt  AdmissionReceipt
		hold     *quota.Hold
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)
			response, receipt, hold, err := s.DispatchWithQuota(ctx, binding, req, transport, ledger, quota.OpVideosList)
			results <- result{response, receipt, hold, err}
		}()
	}
	close(start)
	admitted, refused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil {
			admitted++
			if r.hold == nil || r.receipt.AdmittedAt.IsZero() {
				t.Fatal("missing admission")
			}
			r.response.Body.Close()
			if err := r.hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeSuccess, sql.NullInt64{}); err != nil {
				t.Fatal(err)
			}
		} else {
			refused++
			if !errors.Is(r.err, ErrQuotaRefused) || r.hold != nil || !r.receipt.AdmittedAt.IsZero() {
				t.Fatalf("bad refusal: %v hold=%v receipt=%+v", r.err, r.hold, r.receipt)
			}
		}
	}
	if admitted != 1 || refused != 1 || sends.Load() != 1 {
		t.Fatalf("admitted=%d refused=%d sends=%d", admitted, refused, sends.Load())
	}
}
