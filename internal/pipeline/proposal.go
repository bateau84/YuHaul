package pipeline

// proposal.go implements the ADR-012 proposal-only route identity anchors for
// dry-run drafts and backtest proposals.
//
// A proposal configuration crosses the public API boundary with each route
// identified by its stable public UUIDv4, but every persisted route reference
// (route_decision.route_id, ConfigVersion snapshot route IDs) remains the
// internal integer route key (ADR-011/ARS-004). Proposal ingest resolves that
// translation in one SQLite transaction together with any identity anchors,
// the immutable translated ConfigVersion snapshot, and the run row, so a
// failed proposal never leaves a partial mapping, anchor, snapshot, or run.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/config"
	"github.com/bateau84/YuHaul/internal/router"
	"github.com/bateau84/YuHaul/internal/state"
)

// validateProposalRouteIDs enforces the ADR-012 §1 input rules on a proposal
// configuration: every routes[].id must be a canonical UUIDv4 (uppercase hex
// accepted, canonical lowercase returned) and no UUID may repeat within one
// proposal. Display names are never used to resolve identity, so duplicate
// names stay independent. There is no numeric alias.
func validateProposalRouteIDs(proposal *config.ProposalSnapshot) error {
	seen := make(map[string]int, len(proposal.Routes))
	for i := range proposal.Routes {
		canonical, ok := router.ParsePublicRouteID(proposal.Routes[i].ID)
		if !ok {
			return fmt.Errorf("route %d: invalid route id", i)
		}
		if first, dup := seen[canonical]; dup {
			return fmt.Errorf("route %d: duplicate route id (already used by route %d)", i, first)
		}
		seen[canonical] = i
		proposal.Routes[i].ID = canonical
	}
	return nil
}

// ingestProposalRun executes the ADR-012 proposal-ingest transaction: each
// proposal route UUID is resolved through route_identity (recreating or
// creating an inactive identity anchor and/or the registry mapping when
// required), the translated immutable ConfigVersion snapshot is stored, and
// the run row is written — all committed atomically or not at all. On success
// run carries its ConfigVersionID and ID. A repeated unknown UUID races only
// with itself: a uniqueness conflict rolls the whole transaction back and the
// next attempt reuses the winning mapping instead of replacing it (ADR-012
// §5).
func (p *Pipeline) ingestProposalRun(ctx context.Context, description string,
	proposal *config.ProposalSnapshot, run *state.Run) error {

	if p.configStore == nil {
		return fmt.Errorf("pipeline: proposal configuration requires a config store")
	}
	if p.db == nil {
		return fmt.Errorf("pipeline: proposal configuration requires a database")
	}
	if err := validateProposalRouteIDs(proposal); err != nil {
		return err
	}

	const maxAttempts = 3
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err = p.tryIngestProposalRun(ctx, description, proposal, run)
		if err == nil {
			return nil
		}
		if !isProposalIdentityRace(err) {
			return err
		}
	}
	return err
}

func (p *Pipeline) tryIngestProposalRun(ctx context.Context, description string,
	proposal *config.ProposalSnapshot, run *state.Run) error {

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("pipeline: begin proposal ingest: %w", err)
	}
	defer tx.Rollback()

	// Reserve SQLite's single writer before taking any read snapshot. A
	// deferred read-then-write transaction can otherwise fail its upgrade with
	// SQLITE_BUSY_SNAPSHOT if finalization or another proposal commits between
	// the identity lookup and our writes. This zero-row write changes no data
	// but acquires the reservation using the connection's bounded busy timeout,
	// while retaining *sql.Tx for all atomic anchors/mappings/snapshot/run work.
	if _, err := tx.ExecContext(ctx, `UPDATE run SET id = id WHERE 0`); err != nil {
		return fmt.Errorf("pipeline: reserve proposal writer: %w", err)
	}

	snapshot := config.Snapshot{
		Routes:             make([]config.SnapshotRoute, 0, len(proposal.Routes)),
		DuplicateThreshold: proposal.DuplicateThreshold,
		// Legacy request strings are accepted but never captured as policy.
		DuplicateBehavior: "continue",
	}
	for _, pr := range proposal.Routes {
		key, err := p.proposalRouteKey(ctx, tx, pr)
		if err != nil {
			return err
		}
		snapshot.Routes = append(snapshot.Routes, config.SnapshotRoute{
			ID:                   key,
			Name:                 pr.Name,
			Description:          pr.Description,
			EvaluationOrder:      pr.EvaluationOrder,
			IsFinal:              pr.IsFinal,
			IsEnabled:            pr.IsEnabled,
			ConditionComposition: pr.ConditionComposition,
			Conditions:           pr.Conditions,
			Actions:              pr.Actions,
			Exclusions:           pr.Exclusions,
		})
	}

	cfgID, err := p.configStore.CreateTx(ctx, tx, snapshot, description)
	if err != nil {
		return fmt.Errorf("pipeline: persist proposal config: %w", err)
	}
	run.ConfigVersionID = &cfgID

	runID, err := p.stateStore.CreateRunTx(ctx, tx, run)
	if err != nil {
		return fmt.Errorf("pipeline: create proposal run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("pipeline: commit proposal ingest: %w", err)
	}
	run.ID = runID
	return nil
}

// proposalRouteKey resolves one proposal route UUID to its internal integer
// route key within the ingest transaction (ADR-012 §2-4). An existing mapping
// is reused without mutating the mapping or any existing route row; a mapped
// key whose route row is absent gets an identity anchor recreated at exactly
// that key; an unknown UUID allocates a new internal key and registers the
// supplied UUID — never a replacement public ID.
func (p *Pipeline) proposalRouteKey(ctx context.Context, tx *sql.Tx, pr config.ProposalRoute) (int64, error) {
	var key int64
	err := tx.QueryRowContext(ctx,
		`SELECT route_id FROM route_identity WHERE public_id = ?;`, pr.ID).Scan(&key)
	switch {
	case err == nil:
		if err := ensureProposalAnchor(ctx, tx, key, pr); err != nil {
			return 0, err
		}
		return key, nil
	case errors.Is(err, sql.ErrNoRows):
		// Unknown UUID: allocate a new internal key below.
	default:
		return 0, fmt.Errorf("pipeline: resolve proposal route %s: %w", pr.ID, err)
	}

	key, err = insertProposalAnchor(ctx, tx, 0, pr)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO route_identity (route_id, public_id) VALUES (?, ?);`, key, pr.ID); err != nil {
		return 0, fmt.Errorf("pipeline: register proposal route identity %s: %w", pr.ID, err)
	}
	return key, nil
}

// ensureProposalAnchor guarantees the mapped internal key has a route row so
// route_decision foreign keys to route.id remain valid. An existing row —
// active, soft-deleted, or a previous anchor — is left completely unchanged
// (ADR-012 §2); only a mapped key whose route row is absent (snapshot-only
// identity from an old hard delete) gets an anchor recreated at exactly that
// key (ADR-012 §3).
func ensureProposalAnchor(ctx context.Context, tx *sql.Tx, key int64, pr config.ProposalRoute) error {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM route WHERE id = ?;`, key).Scan(&n)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		_, err := insertProposalAnchor(ctx, tx, key, pr)
		return err
	default:
		return fmt.Errorf("pipeline: check route row for proposal route %d: %w", key, err)
	}
}

// insertProposalAnchor writes one inactive identity-anchor route row
// (ADR-012 §4): soft-deleted, disabled, and ordered at the -id sentinel used
// by ADR-003 soft deletion. Name, description, is_final, and
// condition_composition are copied from the proposal route as identity-row
// metadata; timestamps are set in the caller's transaction. The full evaluated
// route definition lives only in the run's ConfigVersion snapshot. The anchor
// receives no condition/action/exclusion child rows and is written with
// direct SQL so the route-mutation ConfigVersion callback never runs. A
// non-zero id recreates the anchor at exactly that existing internal key.
func insertProposalAnchor(ctx context.Context, tx *sql.Tx, id int64, pr config.ProposalRoute) (int64, error) {
	now := time.Now().UTC()

	if id != 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO route (id, name, description, evaluation_order, is_final, is_enabled,
			                   condition_composition, created_at, updated_at, deleted_at)
			 VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?);`,
			id, pr.Name, pr.Description, -id, pr.IsFinal, pr.ConditionComposition, now, now, now); err != nil {
			return 0, fmt.Errorf("pipeline: recreate identity anchor for route %d: %w", id, err)
		}
		return id, nil
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO route (name, description, evaluation_order, is_final, is_enabled,
		                   condition_composition, created_at, updated_at, deleted_at)
		 VALUES (?, ?, ?, ?, 0, ?, ?, ?, ?);`,
		pr.Name, pr.Description, 0, pr.IsFinal, pr.ConditionComposition, now, now, now)
	if err != nil {
		return 0, fmt.Errorf("pipeline: insert proposal identity anchor: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("pipeline: read proposal anchor id: %w", err)
	}
	// The -id inactive sentinel (ADR-003) needs the allocated key, so the
	// placeholder evaluation_order is corrected in the same transaction.
	if _, err := tx.ExecContext(ctx,
		`UPDATE route SET evaluation_order = -id WHERE id = ?;`, id); err != nil {
		return 0, fmt.Errorf("pipeline: order proposal identity anchor %d: %w", id, err)
	}
	return id, nil
}

// isProposalIdentityRace reports whether err is the route_identity unique
// constraint failure two concurrent proposal ingests can produce when both
// carry the same previously unknown UUID. The losing transaction rolls back
// whole, so retrying reloads and reuses the winning mapping (ADR-012 §5).
func isProposalIdentityRace(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") && strings.Contains(msg, "route_identity")
}
