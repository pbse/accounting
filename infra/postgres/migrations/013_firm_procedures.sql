BEGIN;
CREATE TABLE procedure_versions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL DEFAULT '',
 kind text NOT NULL CHECK(kind IN ('prepaid','reconciliation')),
 instructions text NOT NULL CHECK(length(instructions) BETWEEN 1 AND 4000),
 expense_account text NOT NULL DEFAULT '',
 prepaid_account text NOT NULL DEFAULT '',
 enabled boolean NOT NULL,
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX procedure_versions_current ON procedure_versions(organization_id,lower(client_entity),kind,id DESC);
ALTER TABLE evidence ADD COLUMN procedure_id bigint REFERENCES procedure_versions(id);
COMMIT;
