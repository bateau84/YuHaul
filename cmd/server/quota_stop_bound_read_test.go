package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bateau84/YuHaul/internal/auth"
	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/bateau84/YuHaul/internal/youtube"
)

// ARS-001 Q5: the bound-read adapter passes no run to Finalize. A videos.list
// quota refusal inside a run must still be recorded with that run's ID, so it
// stops the run.
func TestBoundReadQuotaRefusalStopsItsRun(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`)
	}))
	defer upstream.Close()
	p, db := newTestPlaylistProvider(t, upstream.URL)
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `INSERT INTO run (started_at, run_type, status) VALUES (?, 'manual', 'running')`, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	ctx = quota.WithRun(ctx, runID)
	binding, err := p.auth.Bind(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client := youtube.NewAuthorizedClient(ctx, nil, append(boundReadOptions(p.auth, binding, p.ledger), youtube.WithBaseURL(upstream.URL))...)

	if _, _, err := client.GetVideosWithReturnedIDs(ctx, []string{"v1"}); !youtube.IsQuotaExceeded(err) {
		t.Fatalf("first read error = %v, want provider quotaExceeded", err)
	}
	var attributed int
	var units int
	if err := db.QueryRow(`SELECT count(*), coalesce(sum(units_consumed),0) FROM quota_ledger WHERE run_id = ? AND outcome_category = 'quota'`, runID).Scan(&attributed, &units); err != nil || attributed != 1 || units != 1 {
		t.Fatalf("quota rows for the run = %d, units = %d, err = %v; want 1 row of 1 unit", attributed, units, err)
	}

	// The run's next admitted call is refused before any request is sent.
	_, _, err = client.GetVideosWithReturnedIDs(ctx, []string{"v2"})
	if !errors.Is(err, auth.ErrQuotaRefused) || calls.Load() != 1 {
		t.Fatalf("second read error = %v, upstream calls = %d; want ErrQuotaRefused and 1 call", err, calls.Load())
	}
	if h, err := p.ledger.Acquire(ctx, quota.OpPlaylistItemsInsert); err != nil || h != nil {
		t.Fatalf("insert admission after the stop = %v, %v; want refusal", h, err)
	}
	// An unscoped call, outside any run, is still admitted.
	if h, err := p.ledger.Acquire(context.Background(), quota.OpVideosList); err != nil || h == nil {
		t.Fatalf("unscoped admission = %v, %v", h, err)
	} else {
		_ = h.Cancel()
	}
}
