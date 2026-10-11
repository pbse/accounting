BEGIN;
-- Account coverage for a month whose books are its files (docs/books-sources.md, delivery step 9): an account's coverage
-- decision and an account hand-off name the books version they were made on, as they name a QuickBooks or Xero capture.
-- Each still names exactly one capture; the others are unchanged.
ALTER TABLE coverage_bindings ADD COLUMN file_version_id bigint REFERENCES books_versions(id) ON DELETE CASCADE;
ALTER TABLE coverage_bindings DROP CONSTRAINT coverage_bindings_capture;
ALTER TABLE coverage_bindings ADD CONSTRAINT coverage_bindings_capture CHECK (num_nonnulls(snapshot_id,xero_snapshot_id,file_version_id)=1);
CREATE INDEX coverage_bindings_file ON coverage_bindings(close_id,file_version_id,account_id,id DESC) WHERE file_version_id IS NOT NULL;
ALTER TABLE goal_handoffs ADD COLUMN file_version_id bigint;
ALTER TABLE goal_handoffs DROP CONSTRAINT goal_handoff_subject;
ALTER TABLE goal_handoffs ADD CONSTRAINT goal_handoff_subject CHECK ((job_id IS NULL) <> (account_id IS NULL)
 AND (account_id IS NULL) = (num_nonnulls(snapshot_id,xero_snapshot_id,file_version_id)=0) AND num_nonnulls(snapshot_id,xero_snapshot_id,file_version_id)<=1);
COMMIT;
