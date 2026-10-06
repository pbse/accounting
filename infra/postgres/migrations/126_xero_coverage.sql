BEGIN;
-- Account coverage for a client whose books are read from Xero (phase A, B01/B03): an account's coverage decision and
-- an account hand-off name the Xero capture they were made on, as they name a QuickBooks capture. Each names exactly
-- one capture; QuickBooks rows are unchanged.
ALTER TABLE coverage_bindings ALTER COLUMN snapshot_id DROP NOT NULL;
ALTER TABLE coverage_bindings ADD COLUMN xero_snapshot_id bigint REFERENCES xero_snapshots(id) ON DELETE CASCADE;
ALTER TABLE coverage_bindings ADD CONSTRAINT coverage_bindings_capture CHECK ((snapshot_id IS NULL) <> (xero_snapshot_id IS NULL));
CREATE INDEX coverage_bindings_xero ON coverage_bindings(close_id,xero_snapshot_id,account_id,id DESC) WHERE xero_snapshot_id IS NOT NULL;
ALTER TABLE goal_handoffs ADD COLUMN xero_snapshot_id bigint;
ALTER TABLE goal_handoffs DROP CONSTRAINT goal_handoff_subject;
ALTER TABLE goal_handoffs ADD CONSTRAINT goal_handoff_subject CHECK ((job_id IS NULL) <> (account_id IS NULL)
 AND (account_id IS NULL) = (snapshot_id IS NULL AND xero_snapshot_id IS NULL) AND (snapshot_id IS NULL OR xero_snapshot_id IS NULL));
COMMIT;
