-- Existing runs cannot attest that they were created by the guarded mutation pipeline.
ALTER TABLE run ADD COLUMN mutation_safety_version INTEGER NOT NULL DEFAULT 0
    CHECK(mutation_safety_version IN (0, 1));
