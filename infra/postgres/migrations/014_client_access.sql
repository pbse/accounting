BEGIN;
CREATE TABLE client_access (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL CHECK(length(subject) BETWEEN 1 AND 200),
 client_entity text NOT NULL CHECK(length(client_entity) BETWEEN 1 AND 200),
 granted boolean NOT NULL,
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX client_access_current ON client_access(organization_id,subject,lower(client_entity),id DESC);
CREATE VIEW current_client_access AS SELECT DISTINCT ON(organization_id,subject,lower(client_entity)) * FROM client_access ORDER BY organization_id,subject,lower(client_entity),id DESC;
COMMIT;
