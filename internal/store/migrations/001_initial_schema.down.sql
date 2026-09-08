PRAGMA foreign_keys = OFF;

DROP TRIGGER IF EXISTS title_fingerprint_fts_insert;
DROP TRIGGER IF EXISTS title_fingerprint_fts_delete;
DROP TRIGGER IF EXISTS title_fingerprint_fts_update;
DROP TABLE IF EXISTS title_fingerprint_fts;

DROP TABLE IF EXISTS advisory;
DROP TABLE IF EXISTS retention_signal;
DROP TABLE IF EXISTS retention_playlist;
DROP TABLE IF EXISTS deferred_item;
DROP TABLE IF EXISTS error_entry;
DROP TABLE IF EXISTS quota_ledger;
DROP TABLE IF EXISTS duplicate_match;
DROP TABLE IF EXISTS executed_action;
DROP TABLE IF EXISTS condition_audit;
DROP TABLE IF EXISTS route_decision;
DROP TABLE IF EXISTS route_exclusion;
DROP TABLE IF EXISTS route_action;
DROP TABLE IF EXISTS route_condition;
DROP TABLE IF EXISTS route;
DROP TABLE IF EXISTS video;
DROP TABLE IF EXISTS channel;
DROP TABLE IF EXISTS title_fingerprint;
DROP TABLE IF EXISTS transient_metadata;
DROP TABLE IF EXISTS run;
DROP TABLE IF EXISTS config_version;

PRAGMA foreign_keys = ON;
