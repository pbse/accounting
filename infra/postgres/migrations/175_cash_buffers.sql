BEGIN;
-- A client's agreed minimum cash buffer, in its home currency, as a firm reviewer approved it: each approval is a new row
-- and the newest is in force. Rows are only ever added, so the cash plan (and every review that froze it) names the
-- version it was compared with. The agent may propose an amount with its basis; only an approval makes it a buffer.
CREATE TABLE cash_buffers (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (char_length(client_entity) BETWEEN 1 AND 300),
 amount numeric(15,2) NOT NULL CHECK (amount >= 0),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 basis text NOT NULL CHECK (char_length(basis) BETWEEN 5 AND 1000),
 proposed_by text NOT NULL CHECK (char_length(proposed_by) BETWEEN 1 AND 300),
 approved_by text NOT NULL CHECK (char_length(approved_by) BETWEEN 1 AND 300),
 approved_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cash_buffers_client ON cash_buffers(organization_id,lower(client_entity),id DESC);
ALTER TABLE cash_buffers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON cash_buffers FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON cash_buffers FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
