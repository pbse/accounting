BEGIN;
-- A client's Profit and Loss budget as imported or revised: monthly amounts per budget line, each mapped to an account
-- of one ledger company. Every save is a new version; a person approves one as the baseline, and an approved version never changes,
-- so the original plan stays reproducible beside its revisions.
CREATE TABLE budget_versions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
 company text NOT NULL CHECK (length(company) BETWEEN 1 AND 300),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 lines jsonb NOT NULL CHECK (jsonb_typeof(lines) = 'array'),
 digest text NOT NULL,
 source text NOT NULL CHECK (length(source) BETWEEN 1 AND 300),
 based_on bigint REFERENCES budget_versions(id),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 approved_by text,
 approved_at timestamptz,
 CHECK ((approved_by IS NULL) = (approved_at IS NULL))
);
CREATE INDEX budget_versions_client ON budget_versions(organization_id, lower(client_entity), id DESC);
ALTER TABLE budget_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON budget_versions USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
