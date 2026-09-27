BEGIN;
-- A retired alias no longer identifies the client; it stays listed with who retired it, when and why.
ALTER TABLE client_aliases ADD COLUMN retired_by text, ADD COLUMN retired_at timestamptz, ADD COLUMN retire_reason text CHECK (length(retire_reason) <= 2000);
DROP INDEX client_aliases_client;
CREATE UNIQUE INDEX client_aliases_client ON client_aliases(organization_id,lower(client_entity),alias) WHERE retired_at IS NULL;
COMMIT;
