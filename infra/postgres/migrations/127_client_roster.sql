BEGIN;
-- The firm's clients and its monthly close template (B01/B02): which clients owe a close each month, and the due day and
-- owner each month's close takes. Both are histories whose latest row per client (or per firm) is current.
--
-- A roster row says whether the client is active, the first month it owes (since), and its explicit overrides of the
-- firm template: due_days and owner, NULL to follow the template (due_days 0: no due date, and '' owner: nobody,
-- whatever the template says).
CREATE TABLE client_roster (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 active boolean NOT NULL,
 since text NOT NULL CHECK (since ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 due_days integer CHECK (due_days BETWEEN 0 AND 60),
 owner text CHECK (length(owner) <= 200),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX client_roster_current ON client_roster(organization_id,lower(client_entity),id DESC);
-- The current entry of each client is its latest row by client identity, so a name later confirmed as an alias of a
-- rostered client is that client, whichever name its latest decision used. It is shown under the client's own name.
CREATE VIEW current_client_roster WITH (security_invoker=true) AS
 SELECT DISTINCT ON (r.organization_id,i.client) r.id,r.organization_id,
  CASE WHEN i.client=lower(r.client_entity) THEN r.client_entity
   ELSE COALESCE((SELECT min(a.client_entity) FROM client_aliases a WHERE a.organization_id=r.organization_id AND a.retired_at IS NULL AND lower(a.client_entity)=i.client),r.client_entity) END AS client_entity,
  r.active,r.since,r.due_days,r.owner,r.reason,r.actor,r.created_at
 FROM client_roster r CROSS JOIN LATERAL (SELECT client_identity(r.organization_id,r.client_entity) AS client) i
 ORDER BY r.organization_id,i.client,r.id DESC;
ALTER TABLE client_roster ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON client_roster FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON client_roster FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

-- The firm's monthly close template: each month's close is due due_days after the month ends (NULL: no due date) and
-- goes to owner ('' : nobody).
CREATE TABLE close_templates (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 due_days integer CHECK (due_days BETWEEN 1 AND 60),
 owner text NOT NULL DEFAULT '' CHECK (length(owner) <= 200),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX close_templates_current ON close_templates(organization_id,id DESC);
ALTER TABLE close_templates ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON close_templates FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON close_templates FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

-- A deletion receipt also keeps a digest of the client the close belonged to (its client identity when deleted), never
-- the name, so a month deleted under any of a client's names stays done on the roster. Older receipts have only the
-- digest of the close's own name.
ALTER TABLE close_deletion_receipts ADD COLUMN client_digest text CHECK (length(client_digest)=64);

-- The roster starts with every client that has a close. A client with a close of one of the three months before this one
-- is active, owing every month from its earliest close of the last twelve, so a month missing between its recent closes
-- shows while older history does not. Any other is listed inactive, from its latest close, for an administrator to
-- confirm rather than left off.
-- A client is one client identity (client_identity), however many confirmed aliases its closes were opened under, and is
-- named by its own name where a close used it.
WITH clients AS (
 SELECT c.organization_id,i.client,max(c.period) AS latest,
  min(c.period) FILTER (WHERE c.period>=to_char(date_trunc('month',now()) - interval '12 months','YYYY-MM')) AS recent_first,
  (array_agg(c.entity ORDER BY lower(c.entity)=i.client DESC,c.period DESC,c.id DESC))[1] AS name
 FROM client_closes c CROSS JOIN LATERAL (SELECT client_identity(c.organization_id,c.entity) AS client) i
 GROUP BY c.organization_id,i.client
), recent AS (SELECT *,latest>=to_char(date_trunc('month',now()) - interval '3 months','YYYY-MM') AS active FROM clients)
INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor)
SELECT organization_id,name,active,CASE WHEN active THEN recent_first ELSE latest END,
 CASE WHEN active THEN 'On the roster when it began: a close in the last three months'
  ELSE 'To confirm: no close in the three months before the roster began, so it is listed inactive until an administrator says it is still a client' END,'migration'
 FROM recent;
COMMIT;
