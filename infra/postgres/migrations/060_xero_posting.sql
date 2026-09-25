BEGIN;
-- A person's grant lets one client's Xero organisation receive approved manual journals, like production_posting_grants.
CREATE TABLE xero_posting_grants (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 connection_id bigint NOT NULL REFERENCES xero_connections(id),
 tenant_id text NOT NULL,
 account_ids text[] NOT NULL CHECK (cardinality(account_ids) BETWEEN 2 AND 20),
 kinds text[] NOT NULL CHECK (cardinality(kinds) >= 1),
 max_amount numeric(18,2) NOT NULL CHECK (max_amount > 0),
 expires_at timestamptz NOT NULL,
 reason text NOT NULL,
 granted_by text NOT NULL,
 granted_at timestamptz NOT NULL DEFAULT now(),
 revoked_by text,
 revoked_at timestamptz
);
CREATE UNIQUE INDEX xero_posting_current_grant ON xero_posting_grants(organization_id,lower(client_entity)) WHERE revoked_at IS NULL;
ALTER TABLE xero_posting_grants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON xero_posting_grants USING (current_setting('app.organization',true) IN ('*',organization_id));

-- One saved intent and at most one attempt per draft and per source, like production_postings.
CREATE TABLE xero_postings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 workpaper_id bigint NOT NULL UNIQUE REFERENCES workpapers(id),
 source_key text NOT NULL,
 grant_id bigint NOT NULL REFERENCES xero_posting_grants(id),
 connection_id bigint NOT NULL REFERENCES xero_connections(id),
 tenant_id text NOT NULL,
 request_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 digest text NOT NULL,
 journal jsonb NOT NULL,
 ledger_before jsonb NOT NULL,
 state text NOT NULL CHECK (state IN ('preview','approved','executing','unknown','verified')),
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 external_id text,
 readback jsonb,
 last_error text NOT NULL DEFAULT '',
 verified_at timestamptz,
 UNIQUE (close_id, source_key)
);
ALTER TABLE xero_postings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON xero_postings USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
