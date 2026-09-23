BEGIN;
ALTER TABLE quickbooks_ledger_selections ADD COLUMN search_query text NOT NULL DEFAULT '' CHECK (length(search_query) <= 100);
ALTER TABLE quickbooks_ledger_selections DROP CONSTRAINT quickbooks_ledger_selections_pkey;
ALTER TABLE quickbooks_ledger_selections ADD PRIMARY KEY (snapshot_id, account_id, search_query, offset_rows);
COMMIT;
