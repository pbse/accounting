BEGIN;
-- The firm's clients and its monthly close template (B01/B02): which clients owe a close each month, and the due day and
-- owner each month's close takes. Both are histories whose latest row per client (or per firm) is current.
--
-- A roster row says whether the client is active, the first month it owes (since), and its explicit overrides of the
-- firm template: due_days and owner, NULL to follow the template ('' owner: nobody, whatever the template says).
CREATE TABLE client_roster (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 active boolean NOT NULL,
 since text NOT NULL CHECK (since ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 due_days integer CHECK (due_days BETWEEN 1 AND 60),
 owner text CHECK (length(owner) <= 200),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX client_roster_current ON client_roster(organization_id,lower(client_entity),id DESC);
CREATE VIEW current_client_roster WITH (security_invoker=true) AS SELECT DISTINCT ON (organization_id,lower(client_entity)) * FROM client_roster ORDER BY organization_id,lower(client_entity),id DESC;
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

-- The roster starts with every client that has a close of one of the three months before this one: each owes the months
-- after its latest close, so gaps further back are not reported as missing.
INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor)
SELECT DISTINCT ON (c.organization_id,lower(c.entity)) c.organization_id,c.entity,true,c.period,'On the roster when it began: a close in the last three months','migration'
 FROM client_closes c
 WHERE c.period>=to_char(date_trunc('month',now()) - interval '3 months','YYYY-MM')
 ORDER BY c.organization_id,lower(c.entity),c.period DESC,c.id DESC;
COMMIT;
