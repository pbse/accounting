BEGIN;
-- A named what-if over a client's cash plan: moves of open items and added receipts or payments, made for one ledger
-- company in its home currency. It never changes the base plan, the open items or the ledger.
CREATE TABLE cash_scenarios (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (char_length(client_entity) BETWEEN 1 AND 200),
 company text NOT NULL CHECK (char_length(company) BETWEEN 1 AND 300),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
 changes jsonb NOT NULL CHECK (jsonb_typeof(changes)='array' AND jsonb_array_length(changes) BETWEEN 1 AND 20),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 retired_by text,
 retired_at timestamptz,
 CHECK ((retired_by IS NULL) = (retired_at IS NULL))
);
CREATE INDEX cash_scenarios_client ON cash_scenarios(organization_id,lower(client_entity));
ALTER TABLE cash_scenarios ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON cash_scenarios USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
