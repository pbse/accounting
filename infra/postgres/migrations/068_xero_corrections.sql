BEGIN;
-- One reviewed recode of one Xero bill line: preview, a person's approval, at most one attempt and a readback, like bill_corrections.
CREATE TABLE xero_corrections (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 snapshot_id bigint NOT NULL REFERENCES xero_snapshots(id),
 grant_id bigint NOT NULL REFERENCES xero_posting_grants(id),
 connection_id bigint NOT NULL REFERENCES xero_connections(id),
 tenant_id text NOT NULL,
 invoice_id text NOT NULL,
 target_line_id text NOT NULL,
 target_account_id text NOT NULL,
 target_account_code text NOT NULL,
 target_account_name text NOT NULL,
 reason text NOT NULL,
 rule_reference text NOT NULL,
 before_bill jsonb NOT NULL,
 digest text NOT NULL,
 request_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 state text NOT NULL CHECK (state IN ('preview','approved','executing','unknown','verified')),
 proposed_by text NOT NULL,
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 readback jsonb,
 last_error text NOT NULL DEFAULT '',
 verified_at timestamptz,
 UNIQUE (tenant_id, invoice_id)
);
ALTER TABLE xero_corrections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON xero_corrections USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
