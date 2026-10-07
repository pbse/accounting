BEGIN;
-- The firm's own statement of cash flows for a close whose books are read from Xero, which has no such report: the CSV
-- as imported, the importer's attestation, and the ledger company and currency of the capture it was made for. The
-- latest row counts, and only while the close's capture is of that company in that currency.
CREATE TABLE cash_flow_imports (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 company text NOT NULL,
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 csv text NOT NULL CHECK (length(csv) <= 65536),
 source text NOT NULL CHECK (length(source) BETWEEN 1 AND 300),
 attestation text NOT NULL,
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cash_flow_imports_latest ON cash_flow_imports(close_id,id DESC);
ALTER TABLE cash_flow_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON cash_flow_imports USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
