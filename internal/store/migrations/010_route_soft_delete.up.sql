-- Add soft-delete support to the route table.
-- Soft-deleted routes are excluded from management and evaluation queries
-- but their rows are retained so route_decision / condition_audit /
-- executed_action evidence and historical statistics survive deletion.
ALTER TABLE route ADD COLUMN deleted_at TIMESTAMP;

CREATE INDEX IF NOT EXISTS idx_route_deleted_at ON route(deleted_at);
