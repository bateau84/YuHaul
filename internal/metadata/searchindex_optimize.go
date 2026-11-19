package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// OptimizeMergePages is the FTS5 merge size of one optimize step, in index
// pages of about 4 KB (ARS-001 §10.10). It is a variable so tests can force
// small steps, and so the release gate can record a changed value.
var OptimizeMergePages = 500

// OptimizeMaxSteps caps one OptimizeSearchIndex call (ARS-001 §10.10). A call
// that reaches the cap is incomplete; the next call takes over the work.
var OptimizeMaxSteps = 1000

// OptimizeStats reports one OptimizeSearchIndex call. It holds counts and
// timings only, never terms or keys.
type OptimizeStats struct {
	// Steps counts committed steps.
	Steps int
	// Complete is true only when FTS5 reported that no merge work remained. It
	// is false when ctx ended, a step failed or OptimizeMaxSteps was reached.
	Complete bool
	// Longest is the longest time one step held the write lock.
	Longest time.Duration
	// Total is the wall time of the call.
	Total time.Duration
}

// OptimizeSearchIndex merges the FTS5 index incrementally so that the trigrams
// of removed entries leave the live segments (ARS-001 §10.10, I2b). Each step is
// one write-lock-first transaction on a dedicated connection: BEGIN IMMEDIATE,
// one 'merge' command, COMMIT. The write lock is released between steps.
//
// The first step uses a negative page count, which starts a full merge of the
// existing segments; later steps use the positive count, which continues that
// merge. A step that changes fewer than two rows found no more work, and the
// call is then complete. A failed step rolls back and earlier steps stay
// committed. The call never changes which entries exist.
func OptimizeSearchIndex(ctx context.Context, db *sql.DB) (stats OptimizeStats, _ error) {
	start := time.Now()
	defer func() { stats.Total = time.Since(start) }()
	pages := OptimizeMergePages
	for stats.Steps < OptimizeMaxSteps {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		arg := pages
		if stats.Steps == 0 {
			arg = -pages
		}
		var changed int64
		held, err := inWriteTxHeld(ctx, db, func(c *sql.Conn) error {
			var before, after int64
			if err := c.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
				return fmt.Errorf("metadata: read search index change count: %w", err)
			}
			if _, err := c.ExecContext(ctx, `INSERT INTO title_search(title_search, rank) VALUES('merge', ?)`, arg); err != nil {
				return fmt.Errorf("metadata: merge search index: %w", err)
			}
			if err := c.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
				return fmt.Errorf("metadata: read search index change count: %w", err)
			}
			changed = after - before
			return nil
		})
		if err != nil {
			return stats, err
		}
		// A step counts only after its COMMIT succeeded.
		stats.Steps++
		stats.Longest = max(stats.Longest, held)
		if changed < 2 {
			stats.Complete = true
			return stats, nil
		}
		if err := yieldWriteLock(ctx, held); err != nil {
			return stats, err
		}
	}
	return stats, nil
}
