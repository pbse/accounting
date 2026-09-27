BEGIN;
-- The supported ledger rows of each Xero capture, reshaped like QuickBooks rows, so search reads rows, not whole payloads.
CREATE TABLE xero_transaction_rows (
 snapshot_id bigint NOT NULL REFERENCES xero_snapshots(id) ON DELETE CASCADE,
 kind text NOT NULL,
 provider_id text NOT NULL,
 txn_date date NOT NULL,
 currency text,
 amount text,
 record jsonb NOT NULL,
 PRIMARY KEY (snapshot_id,kind,provider_id)
);
CREATE INDEX xero_transaction_rows_browse ON xero_transaction_rows(snapshot_id,txn_date DESC,kind,provider_id);
ALTER TABLE xero_transaction_rows ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON xero_transaction_rows USING (current_setting('app.organization',true)='*' OR snapshot_id IN (SELECT id FROM xero_snapshots));
COMMIT;
