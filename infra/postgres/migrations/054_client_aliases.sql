BEGIN;
-- Another name a client's documents use, confirmed by a reviewer; stored normalized.
CREATE TABLE client_aliases (
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 alias text NOT NULL,
 confirmed_by text NOT NULL,
 confirmed_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX client_aliases_client ON client_aliases(organization_id,lower(client_entity),alias);
ALTER TABLE client_aliases ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON client_aliases USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
