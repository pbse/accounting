BEGIN;
-- The ledger each client keeps (docs/books-sources.md, Ledger catalog and onboarding): recorded when the client is added
-- and corrected on its page, as a history whose latest row per client is current. ledger is a catalog entry's id
-- (internal/ledgers/catalog.json), 'other' with the name a person gave, or 'unknown' (not known yet). The ledger says
-- which product the client keeps; it is not how a month's books arrive, and correcting it changes no books.
CREATE TABLE client_ledgers (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 ledger text NOT NULL CHECK (ledger ~ '^[a-z0-9_]{1,40}$'),
 other_name text NOT NULL DEFAULT '' CHECK (length(other_name) <= 120 AND ((ledger='other')=(other_name<>''))),
 reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX client_ledgers_current ON client_ledgers(organization_id,lower(client_entity),id DESC);
-- A client's current ledger is its latest row by client identity, so a name later confirmed as an alias is that client.
CREATE VIEW current_client_ledgers WITH (security_invoker=true) AS
 SELECT DISTINCT ON (l.organization_id,client_identity(l.organization_id,l.client_entity)) l.id,l.organization_id,client_identity(l.organization_id,l.client_entity) AS client,
  l.client_entity,l.ledger,l.other_name,l.reason,l.actor,l.created_at
 FROM client_ledgers l ORDER BY l.organization_id,client_identity(l.organization_id,l.client_entity),l.id DESC;
ALTER TABLE client_ledgers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON client_ledgers FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON client_ledgers FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

-- A firm piloting a native connector before it reaches every firm (Connectors are switched on by configuration, per
-- environment and, for a pilot, per firm): staff switch it on or off for one firm with a reason, as a history.
CREATE TABLE connector_pilots (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 connector text NOT NULL CHECK (connector ~ '^[a-z0-9_]{1,40}$'),
 enabled boolean NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX connector_pilots_current ON connector_pilots(organization_id,connector,id DESC);
ALTER TABLE connector_pilots ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON connector_pilots FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
-- Staff write a pilot as the firm it is for (adminAct), as every audited staff action does; firms have no route to it.
CREATE POLICY tenant_add ON connector_pilots FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
