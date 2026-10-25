-- Route public identity registry (ADR-011, ARS-004).
--
-- route.id remains the internal integer key and every existing route foreign
-- key is unchanged; route_identity only maps that key to the stable public
-- UUIDv4. There is deliberately no foreign key to route: the registry must
-- retain identity for historical route IDs that survive only inside immutable
-- ConfigVersion snapshots (route rows hard-deleted before ADR-003).
--
-- UUID assignment is application data, not schema: golang-migrate applies
-- only SQL, so the missing-only backfill and integrity validation run in the
-- startup backfill (store.BackfillRouteIdentities) immediately after this
-- migration and before the API can serve (ARS-004 "Versioned migration and
-- startup backfill").
--
-- Registry rows are append-only and public_id is immutable. The UPDATE and
-- DELETE triggers enforce both rules at the storage layer, so an assigned
-- UUID can never be replaced or regenerated.
CREATE TABLE IF NOT EXISTS route_identity (
    route_id INTEGER PRIMARY KEY,
    public_id TEXT NOT NULL UNIQUE
);

CREATE TRIGGER IF NOT EXISTS route_identity_no_update
BEFORE UPDATE ON route_identity
BEGIN
    SELECT RAISE(ABORT, 'route_identity rows are immutable');
END;

CREATE TRIGGER IF NOT EXISTS route_identity_no_delete
BEFORE DELETE ON route_identity
BEGIN
    SELECT RAISE(ABORT, 'route_identity rows are append-only');
END;

-- Intentionally no 012_route_identity.down.sql: the UUID migration is
-- forward-only for assigned identities. Reversing it would drop the registry
-- and destroy assigned UUID mappings, which ARS-004 forbids.
