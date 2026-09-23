BEGIN;
ALTER TABLE quickbooks_entity_syncs DROP CONSTRAINT quickbooks_entity_syncs_state_check;
ALTER TABLE quickbooks_entity_syncs ADD CONSTRAINT quickbooks_entity_syncs_state_check CHECK (state IN ('queued','running','completed','unchanged','failed'));

CREATE TABLE quickbooks_transaction_rows (
 sync_id bigint NOT NULL REFERENCES quickbooks_entity_syncs(id) ON DELETE CASCADE,
 kind text NOT NULL,
 provider_id text NOT NULL,
 revision text NOT NULL,
 txn_date date NOT NULL,
 provider_updated_at timestamptz NOT NULL,
 currency text,
 amount text,
 record jsonb NOT NULL,
 PRIMARY KEY (sync_id,kind,provider_id)
);
CREATE INDEX quickbooks_transaction_rows_browse ON quickbooks_transaction_rows(sync_id,txn_date DESC,kind,provider_id);
COMMIT;
