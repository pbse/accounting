BEGIN;
CREATE TABLE organizations (
 id text PRIMARY KEY CHECK (id = 'local-development' OR id ~ '^org_[A-Za-z0-9]+$'),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
 enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO organizations(id,name) VALUES ('local-development','Local development');
-- Existing unassigned data is never automatically disclosed to a hosted firm.
ALTER TABLE client_closes ADD COLUMN organization_id text NOT NULL DEFAULT 'local-development' REFERENCES organizations(id);
DROP INDEX client_closes_entity_period;
CREATE UNIQUE INDEX client_closes_entity_period ON client_closes (organization_id,lower(entity),period);
COMMIT;
