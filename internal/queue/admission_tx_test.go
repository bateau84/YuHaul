package queue

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// WithAdmissionTx is the bounded writer scope of the final POST admission.

func TestWithAdmissionTxHoldsTheWriterOnlyForTheScope(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	entered, finish := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.WithAdmissionTx(ctx, 5*time.Second, func(_ context.Context, tx *sql.Tx) error {
			close(entered)
			<-finish
			return nil
		})
	}()
	<-entered
	// Another writer cannot proceed while the scope holds the writer.
	other, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	err := s.WithRetryTx(other, func(*sql.Tx) error { return nil })
	cancel()
	if err == nil {
		t.Fatal("a second writer ran inside the admission scope")
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Released: the writer is free again before the caller does anything else.
	if err := s.WithRetryTx(ctx, func(*sql.Tx) error { return nil }); err != nil {
		t.Fatalf("writer not released after the scope: %v", err)
	}
}

func TestWithAdmissionTxCancelEndsOnlyTheWait(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	t.Run("cancel while waiting for the writer", func(t *testing.T) {
		held, release := make(chan struct{}), make(chan struct{})
		holder := make(chan error, 1)
		go func() {
			holder <- s.WithRetryTx(ctx, func(*sql.Tx) error { close(held); <-release; return nil })
		}()
		<-held
		reqCtx, cancel := context.WithCancel(ctx)
		ran := false
		errc := make(chan error, 1)
		go func() {
			errc <- s.WithAdmissionTx(reqCtx, 30*time.Second, func(context.Context, *sql.Tx) error { ran = true; return nil })
		}()
		cancel()
		if err := <-errc; !errors.Is(err, context.Canceled) || ran {
			t.Fatalf("err=%v ran=%v, want a cancelled wait that never ran", err, ran)
		}
		close(release)
		if err := <-holder; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cancel after the writer is held does not interrupt the scope", func(t *testing.T) {
		reqCtx, cancel := context.WithCancel(ctx)
		err := s.WithAdmissionTx(reqCtx, 5*time.Second, func(txCtx context.Context, tx *sql.Tx) error {
			cancel()
			var n int
			if err := tx.QueryRowContext(txCtx, `SELECT count(*) FROM run`).Scan(&n); err != nil {
				return err // the transaction must survive the request's cancellation
			}
			return nil
		})
		if err != nil {
			t.Fatalf("request cancellation tore the writer scope: %v", err)
		}
	})

	t.Run("a bounded wait runs out", func(t *testing.T) {
		held, release := make(chan struct{}), make(chan struct{})
		holder := make(chan error, 1)
		go func() {
			holder <- s.WithRetryTx(ctx, func(*sql.Tx) error { close(held); <-release; return nil })
		}()
		<-held
		err := s.WithAdmissionTx(ctx, 100*time.Millisecond, func(context.Context, *sql.Tx) error { return nil })
		close(release)
		<-holder
		if err == nil {
			t.Fatal("the wait for the writer was unbounded")
		}
	})

	t.Run("an error rolls back and releases", func(t *testing.T) {
		boom := errors.New("boom")
		if err := s.WithAdmissionTx(ctx, time.Second, func(context.Context, *sql.Tx) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("err=%v", err)
		}
		if err := s.WithRetryTx(ctx, func(*sql.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
	})
}
