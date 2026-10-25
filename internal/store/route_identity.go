package store

// route_identity.go implements the ARS-004/ADR-011 startup backfill for the
// append-only route identity registry created by migration 012.
//
// The registry maps the internal integer route key (route.id) to the stable
// public UUIDv4. Migration 012 creates the schema; this file assigns the
// application data. BackfillRouteIdentities runs immediately after the SQL
// migrations and must complete before the scheduler or HTTP server can start;
// store.Open wires it in that position and fails the open on any error, so an
// incomplete or corrupt registry prevents startup instead of being papered
// over by an integer fallback.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// BackfillRouteIdentities ensures every route identity key recoverable from
// durable tables has exactly one immutable UUIDv4 mapping in route_identity,
// then validates registry integrity. It runs as one SQLite transaction:
//
//  1. Collect the union of route identity keys from route rows (active and
//     soft-deleted), every surviving route-reference column, and the valid
//     numeric routes[].id fields of stored ConfigVersion snapshot JSON.
//  2. Insert a cryptographically random UUIDv4 for each distinct key that has
//     no registry row. Existing mappings are never replaced or regenerated,
//     which makes the backfill idempotent across restarts: a crash after the
//     012 SQL migration is recorded but before backfill commits is simply
//     repeated by the next startup, missing-only.
//  3. Validate that every collected key has a mapping and that the whole
//     registry is canonical UUIDv4, lowercase, version 4, RFC-variant, and
//     distinct. Any failure rolls back and returns an error; the caller
//     (store.Open, and through it cmd/server before scheduler.Start and
//     ListenAndServe) must treat it as fatal. No snapshot or source row is
//     modified.
func BackfillRouteIdentities(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: route identity backfill: begin transaction: %w", err)
	}
	defer tx.Rollback()

	keys, err := collectRouteIdentityKeys(ctx, tx)
	if err != nil {
		return err
	}

	for _, key := range sortedKeys(keys) {
		var mapped int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM route_identity WHERE route_id = ?;`, key,
		).Scan(&mapped); err != nil {
			return fmt.Errorf("store: route identity backfill: read mapping for route %d: %w", key, err)
		}
		if mapped > 0 {
			continue
		}

		publicID, err := uuid.NewRandom()
		if err != nil {
			return fmt.Errorf("store: route identity backfill: generate uuid for route %d: %w", key, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO route_identity (route_id, public_id) VALUES (?, ?);`,
			key, publicID.String(),
		); err != nil {
			// A uniqueness collision aborts the transaction; it never
			// replaces another mapping (ARS-004 failure behavior).
			return fmt.Errorf("store: route identity backfill: insert mapping for route %d: %w", key, err)
		}
	}

	if err := validateRouteIdentityRegistry(ctx, tx, keys); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: route identity backfill: commit: %w", err)
	}
	return nil
}

// routeIdentityReferenceQueries are the durable route-reference sources listed
// by ARS-004. Every referenced key must have a registry mapping before the
// API serves.
var routeIdentityReferenceQueries = []string{
	`SELECT id FROM route;`, // includes soft-deleted rows (ADR-003)
	`SELECT route_id FROM route_condition;`,
	`SELECT route_id FROM route_action;`,
	`SELECT route_id FROM route_exclusion;`,
	`SELECT route_id FROM route_decision;`,
	`SELECT source_route_id FROM duplicate_match WHERE source_route_id IS NOT NULL;`,
}

// collectRouteIdentityKeys gathers the union of route identity keys from the
// route-reference tables and the valid numeric routes[].id fields of every
// stored ConfigVersion snapshot.
func collectRouteIdentityKeys(ctx context.Context, tx *sql.Tx) (map[int64]struct{}, error) {
	keys := make(map[int64]struct{})

	for _, query := range routeIdentityReferenceQueries {
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("store: route identity backfill: collect keys: %w", err)
		}
		for rows.Next() {
			var key int64
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: route identity backfill: scan key: %w", err)
			}
			keys[key] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: route identity backfill: iterate keys: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("store: route identity backfill: close key rows: %w", err)
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT snapshot FROM config_version;`)
	if err != nil {
		return nil, fmt.Errorf("store: route identity backfill: read config snapshots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var snapshot string
		if err := rows.Scan(&snapshot); err != nil {
			return nil, fmt.Errorf("store: route identity backfill: scan config snapshot: %w", err)
		}
		for _, key := range snapshotRouteIDs(snapshot) {
			keys[key] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: route identity backfill: iterate config snapshots: %w", err)
	}

	return keys, nil
}

// snapshotRouteIDs extracts integer route IDs from one stored ConfigVersion
// snapshot without modifying it (OC-003 immutability). Malformed legacy JSON
// is skipped with the existing incomplete-context behavior: the migration
// neither rewrites nor repairs snapshots. Within a structurally valid
// document, a routes[].id that is not a JSON integer (string, float, null,
// absent) is skipped as invalid. Any decoded integer is treated as a
// recoverable identity key, including IDs whose route row no longer exists.
func snapshotRouteIDs(snapshot string) []int64 {
	var doc struct {
		Routes []struct {
			ID json.RawMessage `json:"id"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(snapshot), &doc); err != nil {
		return nil
	}

	ids := make([]int64, 0, len(doc.Routes))
	for _, route := range doc.Routes {
		if len(route.ID) == 0 {
			continue
		}
		// A pointer target distinguishes JSON null (no error, no value)
		// from an actual integer; a null id is not a recoverable key.
		var id *int64
		if err := json.Unmarshal(route.ID, &id); err != nil || id == nil {
			continue
		}
		ids = append(ids, *id)
	}
	return ids
}

// validateRouteIdentityRegistry enforces ARS-004's invariants inside the
// backfill transaction: every collected key has exactly one mapping, every
// public ID is a canonical lowercase hyphenated UUIDv4 with the RFC variant,
// and no UUID maps to more than one internal key.
func validateRouteIdentityRegistry(ctx context.Context, tx *sql.Tx, keys map[int64]struct{}) error {
	for _, key := range sortedKeys(keys) {
		var mapped int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM route_identity WHERE route_id = ?;`, key,
		).Scan(&mapped); err != nil {
			return fmt.Errorf("store: route identity validation: read mapping for route %d: %w", key, err)
		}
		if mapped != 1 {
			return fmt.Errorf("store: route identity validation: route %d has %d mappings, want exactly 1", key, mapped)
		}
	}

	rows, err := tx.QueryContext(ctx, `SELECT route_id, public_id FROM route_identity;`)
	if err != nil {
		return fmt.Errorf("store: route identity validation: read registry: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]int64)
	for rows.Next() {
		var key int64
		var publicID string
		if err := rows.Scan(&key, &publicID); err != nil {
			return fmt.Errorf("store: route identity validation: scan registry: %w", err)
		}
		parsed, err := uuid.Parse(publicID)
		if err != nil || parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 || parsed.String() != publicID {
			return fmt.Errorf("store: route identity validation: route %d public id %q is not a canonical UUIDv4", key, publicID)
		}
		if previous, dup := seen[publicID]; dup {
			return fmt.Errorf("store: route identity validation: public id %q maps to routes %d and %d", publicID, previous, key)
		}
		seen[publicID] = key
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: route identity validation: iterate registry: %w", err)
	}
	return nil
}

// sortedKeys returns the map keys in ascending order so backfill inserts and
// validation iterate deterministically.
func sortedKeys(keys map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}
