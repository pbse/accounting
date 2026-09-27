BEGIN;
-- A hand-off is either a document (job_id) or an account of the ledger capture it was handed on (account_id with that
-- capture's snapshot_id); for an account, document holds "coverage for <account>".
ALTER TABLE goal_handoffs ALTER COLUMN job_id DROP NOT NULL;
ALTER TABLE goal_handoffs ADD COLUMN account_id text;
ALTER TABLE goal_handoffs ADD COLUMN snapshot_id bigint;
ALTER TABLE goal_handoffs ADD CONSTRAINT goal_handoff_subject CHECK ((job_id IS NULL) <> (account_id IS NULL) AND (account_id IS NULL) = (snapshot_id IS NULL));
CREATE UNIQUE INDEX goal_handoff_open_account ON goal_handoffs(goal_id,account_id) WHERE closed_at IS NULL AND account_id IS NOT NULL;
COMMIT;
