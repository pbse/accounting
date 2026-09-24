BEGIN;
-- Set when Intuit reports a change for the connection's company; the scheduler then refreshes early.
ALTER TABLE quickbooks_connections ADD COLUMN changed_at timestamptz;
COMMIT;
