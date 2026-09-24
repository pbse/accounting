BEGIN;
-- A read-only Xero connection per firm and client, opened from one close.
CREATE TABLE xero_connections (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 actor text NOT NULL,
 state text NOT NULL CHECK(state IN ('authorizing','exchanging','choose_tenant','active','refreshing','reconnect_required','disconnected')),
 oauth_digest text NOT NULL,
 oauth_expires_at timestamptz NOT NULL,
 tenants jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(tenants)='array'),
 tenant_id text NOT NULL DEFAULT '',
 tenant_name text NOT NULL DEFAULT '',
 credential bytea,
 access_expires_at timestamptz,
 confirmed_by text,
 confirmed_at timestamptz,
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX xero_current_client ON xero_connections(organization_id,lower(client_entity)) WHERE state<>'disconnected';

CREATE TABLE xero_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 connection_id bigint NOT NULL REFERENCES xero_connections(id),
 tenant_id text NOT NULL,
 period text NOT NULL,
 digest text NOT NULL,
 payload jsonb NOT NULL,
 captured_at timestamptz NOT NULL DEFAULT now(),
 actor text NOT NULL
);

-- Same row-level security as migration 047.
ALTER TABLE xero_connections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON xero_connections USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE xero_snapshots ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON xero_snapshots USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
