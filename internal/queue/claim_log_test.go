package queue

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

// The "queue item closed: video final" line goes through the store's logger
// (the app's JSON logger) and carries run_id when a run does the closing.
func TestFamilyClosedLogIsJSONWithRunID(t *testing.T) {
	var buf bytes.Buffer
	s := newTestStore(t).WithLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	origin, run := retryRun(t, s.db), retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "log-final")
	setFinal(t, s.db, video, "accepted")
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)

	if _, r := claimResult(t, s, item, run); r != ClaimClosedFinal {
		t.Fatalf("result = %v", r)
	}
	var line map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("log line is not JSON: %q: %v", buf.String(), err)
	}
	if line["msg"] != "queue item closed: video final" || line["run_id"] != float64(run) || line["final_outcome"] != "accepted" {
		t.Fatalf("log line = %v", line)
	}

	// A refused user retry has no run yet: JSON, and no run_id.
	buf.Reset()
	other := deferredSibling(t, s, seedVideo(t, s.db, origin, "log-final-2"), origin, DeferredStatusPending)
	setFinal(t, s.db, claimItemVideo(t, s.db, other), "rejected")
	err := s.WithRetryTx(context.Background(), func(tx *sql.Tx) error {
		_, _, err := s.RefuseFinalRetryTx(context.Background(), tx, other, false, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	line = nil
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &line); err != nil {
		t.Fatalf("refusal log line is not JSON: %q: %v", buf.String(), err)
	}
	if _, has := line["run_id"]; has || line["final_outcome"] != "rejected" {
		t.Fatalf("refusal log line = %v", line)
	}
}

func claimItemVideo(t *testing.T, db *sql.DB, item int64) int64 {
	t.Helper()
	var v int64
	if err := db.QueryRow(`SELECT video_id FROM deferred_item WHERE id=?`, item).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
