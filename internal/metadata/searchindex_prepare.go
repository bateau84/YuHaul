package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/bateau84/YuHaul/internal/store"
	"github.com/bateau84/YuHaul/internal/titlekey"
)

// PrepareStats reports one PrepareSearchIndex call. It holds counts only.
type PrepareStats struct {
	// Recreated is true when the index was dropped and recreated because its
	// key version differed from titlekey.Version().
	Recreated bool
	// Reconcile counts what the reconcile changed.
	Reconcile ReconcileStats
	// Optimize is nil when no optimize ran, which is when the reconcile removed
	// no entry.
	Optimize *OptimizeStats
}

// PrepareSearchIndex is the startup step of the title search index (ARS-001
// §10.4). cmd/server calls it right after store.Open and before any worker or
// listener starts. In order, returning at the first error:
//
//  1. when key_version is set and differs from titlekey.Version(), recreate the
//     index (§10.9);
//  2. reconcile it, which is the backfill after a first start or a recreate;
//  3. optimize it, only when the reconcile removed entries
//     (OrphansRemoved + Purged > 0).
//
// An optimize that stops at OptimizeMaxSteps is incomplete, not an error: the
// next sweep continues it, and the caller should log Optimize.Complete. An
// optimize stopped by ctx or by a failed step returns an error and aborts the
// startup. Stats are returned alongside an error for the steps that ran.
func PrepareSearchIndex(ctx context.Context, db *sql.DB, now time.Time) (PrepareStats, error) {
	var stats PrepareStats

	var version sql.NullString
	err := db.QueryRowContext(ctx, `SELECT key_version FROM title_search_state WHERE id = 1`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return stats, fmt.Errorf("metadata: read search index state: %w", err)
	}
	if version.Valid && version.String != titlekey.Version() {
		if err := store.RecreateSearchIndex(ctx, db); err != nil {
			return stats, fmt.Errorf("metadata: recreate search index: %w", err)
		}
		stats.Recreated = true
	}

	rec, err := ReconcileSearchIndex(ctx, db, now)
	stats.Reconcile = rec
	if err != nil {
		return stats, fmt.Errorf("metadata: reconcile search index: %w", err)
	}

	if rec.OrphansRemoved+rec.Purged > 0 {
		opt, err := OptimizeSearchIndex(ctx, db)
		stats.Optimize = &opt
		if err != nil {
			return stats, fmt.Errorf("metadata: optimize search index: %w", err)
		}
	}
	return stats, nil
}
