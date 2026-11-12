package queue

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// BR-039 vocabulary: closed_reason and video_final_outcome are read from the
// row and the video, shown only when closed_reason is set, and serialized.
func TestClosedReasonAndVideoFinalOutcomeAreProjected(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	run := seedRun(t, db)
	closed := seedVideo(t, db, run, "closed")
	open := seedVideo(t, db, run, "open")
	if _, err := db.Exec(`UPDATE video SET final_outcome='rejected', processing_state='completed' WHERE id=?`, closed); err != nil {
		t.Fatal(err)
	}
	// The open video also carries a final outcome; it must not be reported
	// while the row's closed_reason is NULL.
	if _, err := db.Exec(`UPDATE video SET final_outcome='accepted', processing_state='completed' WHERE id=?`, open); err != nil {
		t.Fatal(err)
	}
	var closedDeferred, openDeferred, closedError, openError int64
	for _, c := range []struct {
		id    *int64
		video int64
		table string
	}{{&closedDeferred, closed, "deferred"}, {&openDeferred, open, "deferred"}, {&closedError, closed, "error"}, {&openError, open, "error"}} {
		var res, err = func() (interface{ LastInsertId() (int64, error) }, error) {
			if c.table == "deferred" {
				return db.Exec(`INSERT INTO deferred_item (video_id,run_id,deferral_reason,status) VALUES (?,?, 'quota_exhausted','pending')`, c.video, run)
			}
			return db.Exec(`INSERT INTO error_entry (video_id,run_id,error_category,error_message,status) VALUES (?,?,'runtime','m','active')`, c.video, run)
		}()
		if err != nil {
			t.Fatal(err)
		}
		*c.id, _ = res.LastInsertId()
	}
	for _, q := range []string{
		`UPDATE deferred_item SET status='completed', closed_reason='final_outcome' WHERE id=` + itoa(closedDeferred),
		`UPDATE error_entry SET status='resolved', resolved_at='2026-11-12T00:00:00Z', closed_reason='final_outcome' WHERE id=` + itoa(closedError),
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	d, err := s.GetDeferredItem(ctx, closedDeferred)
	if err != nil {
		t.Fatal(err)
	}
	if d.ClosedReason == nil || *d.ClosedReason != ClosedReasonFinalOutcome || d.VideoFinalOutcome == nil || *d.VideoFinalOutcome != "rejected" {
		t.Errorf("closed deferred item = %+v", d)
	}
	d, err = s.GetDeferredItem(ctx, openDeferred)
	if err != nil {
		t.Fatal(err)
	}
	if d.ClosedReason != nil || d.VideoFinalOutcome != nil {
		t.Errorf("open deferred item reports closure: %+v", d)
	}
	e, err := s.GetErrorEntry(ctx, closedError)
	if err != nil {
		t.Fatal(err)
	}
	if e.ClosedReason == nil || *e.ClosedReason != "final_outcome" || e.VideoFinalOutcome == nil || *e.VideoFinalOutcome != "rejected" {
		t.Errorf("closed error entry = %+v", e)
	}
	e, err = s.GetErrorEntry(ctx, openError)
	if err != nil {
		t.Fatal(err)
	}
	if e.ClosedReason != nil || e.VideoFinalOutcome != nil {
		t.Errorf("open error entry reports closure: %+v", e)
	}

	all, err := s.ListDeferredItems(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("list = %d, %v", len(all), err)
	}
	b, err := json.Marshal(publicDeferredItem(&all[0]))
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["closed_reason"] != "final_outcome" || wire["video_final_outcome"] != "rejected" {
		t.Errorf("wire = %s", b)
	}
	b, _ = json.Marshal(publicDeferredItem(&all[1]))
	wire = map[string]any{}
	_ = json.Unmarshal(b, &wire)
	if v, ok := wire["closed_reason"]; !ok || v != nil {
		t.Errorf("open item must serialize closed_reason:null, got %s", b)
	}
	if _, ok := wire["video_final_outcome"]; ok {
		t.Errorf("open item must omit video_final_outcome: %s", b)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestAlreadyFinalErrorMatchesSentinel(t *testing.T) {
	var err error = &AlreadyFinalError{VideoID: 1, EntryID: 2, Outcome: "accepted"}
	if !errors.Is(err, ErrVideoAlreadyFinal) {
		t.Fatal("AlreadyFinalError must satisfy errors.Is(ErrVideoAlreadyFinal)")
	}
	var af *AlreadyFinalError
	if !errors.As(err, &af) || af.EntryID != 2 {
		t.Fatal("errors.As must recover the entry")
	}
}
