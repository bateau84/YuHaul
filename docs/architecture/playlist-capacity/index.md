# Playlist capacity

- [ARD-001 — Exact-Target Capacity Checks and Bounded Waiting Work](design/ard-001-exact-target-capacity-and-waiting-work.md) — Target architecture for BR-040, BR-041 (including AC7, the outcome after an evicted add) and OC-007. Uses an authorized YouTube count, the existing guarded send path, and atomic target-scoped waiting and eviction in SQLite. Proposed; the 2026-11-22 revision is not yet critiqued.
- [ARS-001 — Playlist Capacity Schema, Waiting Queue, Send Path, Evicted-Add Outcome, APIs and Tests](specifications/ars-001-capacity-schema-queue-and-send-path.md) — Exact contract for ARD-001: the `deferred_item` migration and storage guards, enrollment and eviction, the derived decision pin, the send path, the evicted-add aggregate, queue and run inspection, and tests. Draft; written before the ARD critique.
- [Playlist Capacity Change Log](log.md)
