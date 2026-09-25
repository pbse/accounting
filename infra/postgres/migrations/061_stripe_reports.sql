BEGIN;
-- A client's Stripe account, read through a restricted report-scoped key a person confirmed belongs to the client.
CREATE TABLE stripe_connections (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 actor text NOT NULL,
 state text NOT NULL CHECK (state IN ('unconfirmed','active','disconnected')),
 credential bytea,
 livemode boolean NOT NULL,
 key_last4 text NOT NULL,
 account_id text NOT NULL,
 account_name text NOT NULL DEFAULT '',
 confirmed_by text,
 confirmed_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX stripe_current_client ON stripe_connections(organization_id,lower(client_entity)) WHERE state<>'disconnected';
ALTER TABLE stripe_connections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON stripe_connections USING (current_setting('app.organization',true) IN ('*',organization_id));

-- Each month's report: the intent and its idempotency key are saved before Stripe is asked, so a lost answer is recoverable.
CREATE TABLE stripe_report_runs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 connection_id bigint NOT NULL REFERENCES stripe_connections(id),
 period text NOT NULL,
 idempotency_key uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 run_id text,
 state text NOT NULL CHECK (state IN ('requesting','pending','saved','failed')),
 evidence_id bigint REFERENCES evidence(id),
 error text NOT NULL DEFAULT '',
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 claimed_at timestamptz,
 finished_at timestamptz
);
CREATE UNIQUE INDEX stripe_report_in_flight ON stripe_report_runs(close_id,period) WHERE state IN ('requesting','pending');
ALTER TABLE stripe_report_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON stripe_report_runs USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
