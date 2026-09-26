BEGIN;
-- The newest change time a signed Xero webhook reported for this connection's tenant; a hint, not proof.
ALTER TABLE xero_connections ADD COLUMN changed_at timestamptz;
COMMIT;
