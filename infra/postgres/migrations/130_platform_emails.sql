BEGIN;
-- Krith's own emails to people at firms (docs/sign-in.md#welcome-emails): a welcome to whoever sets up a firm by
-- signing in, and a note to each colleague who joins it by their address. Each row is written in the same transaction
-- as the sign-in that earns it, and the worker sends it through Resend with Krith's key, never a firm's. One email per
-- person and firm; a row is deleted 30 days after it finishes, and with its firm.
CREATE TABLE platform_emails (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 kind text NOT NULL CHECK (kind IN ('welcome','joined')),
 organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 subject text NOT NULL,
 recipient text NOT NULL CHECK (length(recipient) BETWEEN 3 AND 320),
 state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','sending','sent','failed','unknown','expired','cancelled')),
 attempts int NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 claimed_at timestamptz,
 finished_at timestamptz,
 provider_id text,
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (organization_id, subject)
);
CREATE INDEX platform_emails_due ON platform_emails(next_attempt_at) WHERE state IN ('queued','sending');
ALTER TABLE platform_emails ENABLE ROW LEVEL SECURITY;
CREATE POLICY platform ON platform_emails USING (current_setting('app.organization',true)='*');
COMMIT;
