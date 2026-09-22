BEGIN;
CREATE TABLE email_connections (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 sender text NOT NULL,
 domain_id text NOT NULL,
 credential bytea NOT NULL,
 enabled boolean NOT NULL DEFAULT true,
 verified_at timestamptz NOT NULL DEFAULT now(),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX email_connection_enabled ON email_connections(organization_id) WHERE enabled;
CREATE TABLE email_outbox (
 id text PRIMARY KEY,
 request_id bigint NOT NULL UNIQUE REFERENCES work_requests(id) ON DELETE CASCADE,
 connection_id bigint NOT NULL REFERENCES email_connections(id),
 link_digest text NOT NULL REFERENCES client_request_links(digest) ON DELETE CASCADE,
 payload bytea NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','sending','accepted','failed','unknown','cancelled')),
 approved_by text NOT NULL,
 firm_authority boolean NOT NULL,
 approved_until timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 attempts integer NOT NULL DEFAULT 0,
 first_attempt_at timestamptz,
 claim text,
 claimed_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 provider_id text,
 error text NOT NULL DEFAULT '',
 finished_at timestamptz
);
CREATE INDEX email_outbox_pending ON email_outbox(next_attempt_at) WHERE state IN ('queued','sending');
COMMIT;
