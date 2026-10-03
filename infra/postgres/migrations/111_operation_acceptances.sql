BEGIN;
-- Dated evidence for a connector operation that the app cannot record itself: a sandbox check done outside the app
-- (journal kinds checked in a QuickBooks sandbox, Xero operations on a demo company) and the firm's accountant
-- accepting the operation for use. Append-only: the newest row per operation and stage counts, and accepted=false
-- withdraws it. Nothing here enables an operation; production enablement and client grants stay where they are.
CREATE TABLE operation_acceptances (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 provider text NOT NULL CHECK (provider IN ('QuickBooks','Xero','Stripe')),
 kind text NOT NULL CHECK (length(kind) BETWEEN 1 AND 50),
 stage text NOT NULL CHECK (stage IN ('sandbox_checked','accountant_accepted')),
 accepted boolean NOT NULL,
 evidence text NOT NULL CHECK (length(evidence) BETWEEN 5 AND 2000),
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX operation_acceptances_latest ON operation_acceptances(organization_id, provider, kind, stage, id DESC);
ALTER TABLE operation_acceptances ENABLE ROW LEVEL SECURITY;
-- Read and append only: a record is never rewritten or removed by the app.
CREATE POLICY tenant ON operation_acceptances FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_append ON operation_acceptances FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
