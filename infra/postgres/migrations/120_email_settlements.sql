BEGIN;
-- How a person settled, from the provider's own records, an email whose outcome the app could not know, or one a
-- restored backup still shows as queued or sending. Rows are only ever added; one per email.
CREATE TABLE email_settlements (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 outbox_id text NOT NULL UNIQUE REFERENCES email_outbox(id) ON DELETE CASCADE,
 prior_state text NOT NULL CHECK (prior_state IN ('queued','sending','unknown')),
 outcome text NOT NULL CHECK (outcome IN ('accepted','failed')),
 provider_id text CHECK (char_length(provider_id) BETWEEN 1 AND 200),
 evidence text NOT NULL CHECK (char_length(evidence) BETWEEN 5 AND 2000),
 settled_by text NOT NULL,
 settled_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((outcome='accepted') = (provider_id IS NOT NULL))
);
ALTER TABLE email_settlements ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON email_settlements FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON email_settlements FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
