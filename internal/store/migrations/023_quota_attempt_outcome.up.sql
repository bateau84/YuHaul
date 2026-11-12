ALTER TABLE quota_ledger ADD COLUMN outcome_category TEXT
    CHECK (outcome_category IS NULL OR outcome_category IN ('success', 'quota', 'transient', 'auth', 'permanent'));
ALTER TABLE quota_ledger ADD COLUMN http_status INTEGER;
