package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/state"
)

// RequeueItem is one stranded video the user's confirmation gives a deferred
// item, with the production run whose evidence it carries (ARS-005 §3.3).
type RequeueItem struct{ VideoID, OriginRunID int64 }

// requeueChunk is the most pairs one INSERT … SELECT statement carries
// (ARS-005 §4). The chunks share one transaction, so it bounds the statement,
// not the atomicity.
const requeueChunk = 2000

// insertRequeuedSQL gives each planned video one pending, immediately
// eligible item with the reason requeued_by_user (BR-037 AC6). The WHERE
// clause rechecks, under the writer lock, everything that can have changed
// since the scan: a video that finished, gained an active item, was removed by
// the user or holds a send guard is skipped (BR-037 AC3, AC10). ?1 is a JSON
// array of [video_id, origin_run_id] pairs, ?2 the shared created_at.
//
// CROSS JOIN fixes the join order: the pairs are the outer loop and each video
// is a primary-key lookup. With a plain JOIN the planner drives from the video
// table's final_outcome index (the unfinished test is an OR) and rescans the
// whole pair list for every video, which is quadratic in the plan.
const insertRequeuedSQL = `INSERT INTO deferred_item (video_id, run_id, deferral_reason, retry_after, retry_count, status, created_at)
SELECT subject.video_id, subject.origin_run_id, 'requeued_by_user', NULL, 0, 'pending', ?2
FROM (SELECT CAST(json_extract(j.value, '$[0]') AS INTEGER) AS video_id,
             CAST(json_extract(j.value, '$[1]') AS INTEGER) AS origin_run_id
      FROM json_each(?1) j) subject
CROSS JOIN video v ON v.id = subject.video_id
WHERE v.processing_state <> 'completed'
  AND (v.final_outcome IS NULL OR v.final_outcome = 'not_yet_evaluated')
  AND NOT ` + ActiveQueueItem + `
  AND NOT ` + RemovedByUser + `
  AND NOT EXISTS (` + state.HeldSendGuard + `)`

// InsertRequeuedTx inserts the re-queue items of plan in tx, which the caller
// opened under the SQLite writer (WithRetryTx), and returns how many rows it
// wrote. A planned video that no longer qualifies is skipped, so the count may
// be lower than len(plan). It writes nothing but deferred_item rows and makes
// no provider call. Every row shares one created_at.
func (s *Store) InsertRequeuedTx(ctx context.Context, tx *sql.Tx, plan []RequeueItem, now time.Time) (int, error) {
	if tx == nil {
		return 0, errors.New("inserting requeued items: transaction is required")
	}
	createdAt := now.UTC().Format(time.RFC3339)
	inserted := 0
	for start := 0; start < len(plan); start += requeueChunk {
		chunk := plan[start:min(start+requeueChunk, len(plan))]
		pairs := make([][2]int64, len(chunk))
		for i, item := range chunk {
			pairs[i] = [2]int64{item.VideoID, item.OriginRunID}
		}
		list, err := json.Marshal(pairs)
		if err != nil {
			return 0, fmt.Errorf("inserting requeued items: %w", err)
		}
		res, err := tx.ExecContext(ctx, insertRequeuedSQL, string(list), createdAt)
		if err != nil {
			return 0, fmt.Errorf("inserting requeued items: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("inserting requeued items: %w", err)
		}
		inserted += int(n)
	}
	return inserted, nil
}
