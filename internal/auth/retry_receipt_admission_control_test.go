package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/store"
	"modernc.org/sqlite"
)

// This is an auth/quota control, not a retry-identity or writer-fence test.
func TestRetryReceiptAdmissionAccountingControl(t *testing.T) {
	for _, cancelBeforeAdmission := range []bool{true, false} {
		name := "admit_then_transport_loss"
		if cancelBeforeAdmission {
			name = "cancel_during_admission"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			barrier := &scheduleBarrierConnector{drv: &sqlite.Driver{}, entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			defer release.Do(func() { close(barrier.release) })
			db := sql.OpenDB(barrier)
			db.SetMaxOpenConns(1)
			defer db.Close()
			if err := store.ApplyMigrations(db); err != nil {
				t.Fatal(err)
			}
			ledger := quota.New(db)
			if err := ledger.SetDailyLimit(ctx, 1); err != nil {
				t.Fatal(err)
			}
			tokens := newTestStore(t, newTestKey(t))
			authorityTestSeed(t, tokens, time.Now().Add(time.Hour))
			service := authorityTestService(t, tokens, nil)
			binding, err := service.Bind(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack: %v", err)
					return
				}
				conn.Close()
			}))
			defer server.Close()
			callCtx, stop := context.WithCancel(ctx)
			defer stop()
			req, err := http.NewRequestWithContext(callCtx, http.MethodPost, server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				receipt AdmissionReceipt
				hold    *quota.Hold
				err     error
			}
			done := make(chan result, 1)
			go func() {
				resp, receipt, hold, err := service.DispatchWithQuota(callCtx, binding, req, http.DefaultTransport, ledger, quota.OpVideosList)
				if resp != nil {
					resp.Body.Close()
				}
				done <- result{receipt, hold, err}
			}()
			select {
			case <-barrier.entered:
			case r := <-done:
				t.Fatalf("dispatch returned before admission barrier: %v", r.err)
			case <-ctx.Done():
				t.Fatal("admission barrier not reached")
			}
			if cancelBeforeAdmission {
				stop()
			}
			release.Do(func() { close(barrier.release) })
			var r result
			select {
			case r = <-done:
			case <-ctx.Done():
				t.Fatal("dispatch did not return")
			}
			if cancelBeforeAdmission {
				if !errors.Is(r.err, context.Canceled) || r.hold != nil || r.receipt != (AdmissionReceipt{}) || calls.Load() != 0 {
					t.Fatalf("cancelled: err=%v hold=%v receipt=%+v calls=%d", r.err, r.hold, r.receipt, calls.Load())
				}
			} else {
				if r.err == nil || r.hold == nil || r.receipt.AdmittedAt.IsZero() || calls.Load() != 1 {
					t.Fatalf("admitted: err=%v hold=%v receipt=%+v calls=%d", r.err, r.hold, r.receipt, calls.Load())
				}
				if err := r.hold.Finalize(ctx, sql.NullInt64{}, quota.OutcomeTransient, sql.NullInt64{}); err != nil {
					t.Fatal(err)
				}
			}
			var rows, units, attempts int
			if err := db.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(units_consumed), 0), coalesce(sum(call_count), 0) FROM quota_ledger`).Scan(&rows, &units, &attempts); err != nil {
				t.Fatal(err)
			}
			if cancelBeforeAdmission {
				if rows != 0 || units != 0 || attempts != 0 {
					t.Fatalf("cancelled ledger: rows=%d units=%d attempts=%d", rows, units, attempts)
				}
				hold, err := ledger.Acquire(ctx, quota.OpVideosList)
				if err != nil || hold == nil {
					t.Fatalf("capacity not released: hold=%v err=%v", hold, err)
				}
				if err := hold.Cancel(); err != nil {
					t.Fatal(err)
				}
			} else {
				if rows != 1 || units != 1 || attempts != 1 {
					t.Fatalf("admitted ledger: rows=%d units=%d attempts=%d", rows, units, attempts)
				}
				var category string
				var status sql.NullInt64
				if err := db.QueryRowContext(ctx, `SELECT outcome_category, http_status FROM quota_ledger`).Scan(&category, &status); err != nil {
					t.Fatal(err)
				}
				if category != string(quota.OutcomeTransient) || status.Valid {
					t.Fatalf("transport loss: category=%s status=%v", category, status)
				}
			}
		})
	}
}
