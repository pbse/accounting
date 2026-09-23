BEGIN;
CREATE TABLE quickbooks_ledger_selections (
 snapshot_id bigint NOT NULL REFERENCES quickbooks_snapshots(id) ON DELETE CASCADE,
 source_id bigint NOT NULL UNIQUE REFERENCES evidence(id) ON DELETE CASCADE,
 account_id text NOT NULL,
 offset_rows integer NOT NULL CHECK (offset_rows >= 0),
 PRIMARY KEY(snapshot_id,account_id,offset_rows)
);
COMMIT;
