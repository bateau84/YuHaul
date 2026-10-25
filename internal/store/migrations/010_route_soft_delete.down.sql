-- Reverse the route soft-delete column addition.
ALTER TABLE route DROP COLUMN deleted_at;
