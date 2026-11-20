package queue

import (
	"database/sql"
	"fmt"
	"testing"
)

// predicateRow evaluates one shared predicate for a video through the
// documented subject.video_id alias.
func predicateRow(t *testing.T, db *sql.DB, predicate string, videoID int64) bool {
	t.Helper()
	var got bool
	q := fmt.Sprintf(`SELECT %s FROM (SELECT ? AS video_id) subject`, predicate)
	if err := db.QueryRow(q, videoID).Scan(&got); err != nil {
		t.Fatalf("evaluating predicate: %v", err)
	}
	return got
}

func TestQueuePredicatesActiveErrorsAndRemoved(t *testing.T) {
	db := openTestDB(t)
	run := seedRun(t, db)
	deferred := func(video int64, status, at string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_count, status, created_at) VALUES (?,?,?,?,?,?)`,
			video, run, "quota_exhausted", 0, status, at); err != nil {
			t.Fatal(err)
		}
	}
	errEntry := func(video int64, status, at string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO error_entry (video_id,run_id,error_category,error_message,status,created_at) VALUES (?,?,'runtime','x',?,?)`,
			video, run, status, at); err != nil {
			t.Fatal(err)
		}
	}
	vNone := seedVideo(t, db, run, "none")
	vPending := seedVideo(t, db, run, "pending")
	vRetrying := seedVideo(t, db, run, "retrying")
	vActiveErr := seedVideo(t, db, run, "activeerr")
	vRetryErr := seedVideo(t, db, run, "retryerr")
	vDiscarded := seedVideo(t, db, run, "discarded")
	vDismissed := seedVideo(t, db, run, "dismissed")
	vDoneDeferred := seedVideo(t, db, run, "done")
	vDiscardedThenPending := seedVideo(t, db, run, "discthenpend")
	vPendingThenDiscarded := seedVideo(t, db, run, "pendthendisc")
	vTie := seedVideo(t, db, run, "tie")
	vMixedFormat := seedVideo(t, db, run, "mixedformat")

	deferred(vPending, "pending", "2026-01-01T00:00:00Z")
	deferred(vRetrying, "retrying", "2026-01-01T00:00:00Z")
	errEntry(vActiveErr, "active", "2026-01-01T00:00:00Z")
	errEntry(vRetryErr, "retrying", "2026-01-01T00:00:00Z")
	deferred(vDiscarded, "discarded", "2026-01-01T00:00:00Z")
	errEntry(vDismissed, "dismissed", "2026-01-01T00:00:00Z")
	deferred(vDoneDeferred, "completed", "2026-01-01T00:00:00Z")
	deferred(vDiscardedThenPending, "discarded", "2026-01-01T00:00:00Z")
	deferred(vDiscardedThenPending, "pending", "2026-01-02T00:00:00Z")
	deferred(vPendingThenDiscarded, "pending", "2026-01-01T00:00:00Z")
	deferred(vPendingThenDiscarded, "discarded", "2026-01-02T00:00:00Z")
	// Same instant over both queues: the errors-queue item is the later one.
	deferred(vTie, "discarded", "2026-01-03T00:00:00Z")
	errEntry(vTie, "active", "2026-01-03T00:00:00Z")
	// Mixed created_at formats compare by instant. As text, "...T10..." sorts
	// after "... 12..." (T > space), which would pick the wrong item.
	deferred(vMixedFormat, "pending", "2026-01-04T10:00:00Z")
	deferred(vMixedFormat, "discarded", "2026-01-04 12:00:00")

	for _, tc := range []struct {
		name               string
		video              int64
		active, errs, gone bool
	}{
		{"no item", vNone, false, false, false},
		{"pending deferred", vPending, true, false, false},
		{"retrying deferred", vRetrying, true, false, false},
		{"active error", vActiveErr, true, true, false},
		{"retrying error", vRetryErr, true, true, false},
		{"discarded deferred", vDiscarded, false, false, true},
		{"dismissed error", vDismissed, false, false, true},
		{"completed deferred", vDoneDeferred, false, false, false},
		{"discarded then re-queued", vDiscardedThenPending, true, false, false},
		{"queued then discarded", vPendingThenDiscarded, true, false, true},
		{"tie goes to the errors queue", vTie, true, true, false},
		{"mixed created_at formats", vMixedFormat, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := predicateRow(t, db, ActiveQueueItem, tc.video); got != tc.active {
				t.Errorf("ActiveQueueItem = %v, want %v", got, tc.active)
			}
			if got := predicateRow(t, db, ErrorsQueueItem, tc.video); got != tc.errs {
				t.Errorf("ErrorsQueueItem = %v, want %v", got, tc.errs)
			}
			if got := predicateRow(t, db, RemovedByUser, tc.video); got != tc.gone {
				t.Errorf("RemovedByUser = %v, want %v", got, tc.gone)
			}
		})
	}
}
