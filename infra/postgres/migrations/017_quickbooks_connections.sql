BEGIN;
CREATE TABLE quickbooks_connections (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 actor text NOT NULL,
 state text NOT NULL CHECK(state IN ('authorizing','exchanging','pending_confirmation','active','refreshing','reconnect_required','disconnected')),
 oauth_digest text NOT NULL UNIQUE,
 oauth_expires_at timestamptz NOT NULL,
 realm text NOT NULL DEFAULT '',
 company_name text NOT NULL DEFAULT '',
 credential bytea,
 access_expires_at timestamptz,
 confirmed_by text,
 confirmed_at timestamptz,
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX quickbooks_current_client ON quickbooks_connections(organization_id,lower(client_entity)) WHERE state<>'disconnected';
CREATE TABLE quickbooks_snapshots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 connection_id bigint NOT NULL REFERENCES quickbooks_connections(id),
 realm text NOT NULL,
 digest text NOT NULL,
 payload jsonb NOT NULL,
 started_at timestamptz NOT NULL,
 captured_at timestamptz NOT NULL DEFAULT now(),
 actor text NOT NULL
);
COMMIT;
