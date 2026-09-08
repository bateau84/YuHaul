-- Extend route-scoped exclusion types to support minimum duration limits.
ALTER TABLE route_exclusion RENAME TO route_exclusion_old;

CREATE TABLE route_exclusion (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    route_id INTEGER NOT NULL REFERENCES route(id) ON DELETE CASCADE,
    exclusion_type TEXT NOT NULL CHECK(exclusion_type IN ('channel', 'video', 'title', 'word', 'duration_max', 'duration_min')),
    exclusion_value TEXT NOT NULL
);

INSERT INTO route_exclusion (id, route_id, exclusion_type, exclusion_value)
SELECT id, route_id, exclusion_type, exclusion_value FROM route_exclusion_old;

DROP TABLE route_exclusion_old;
