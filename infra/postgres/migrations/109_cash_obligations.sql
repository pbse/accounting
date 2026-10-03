BEGIN;
-- A client's scheduled cash commitment the cash plan cannot read from open invoices and bills: payroll from the
-- payroll provider's schedule, taxes, debt service, rent or another agreed payment or receipt. It counts in the plan
-- only once a person has approved it; a change is a new row that retires the old one, so history stays readable.
CREATE TABLE cash_obligations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
 category text NOT NULL CHECK (category IN ('payroll','tax','debt','rent','other')),
 direction text NOT NULL CHECK (direction IN ('in','out')),
 amount numeric(18,2) NOT NULL CHECK (amount > 0),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 first_on date NOT NULL,
 every text NOT NULL CHECK (every IN ('once','week','two_weeks','month')),
 until_on date CHECK (until_on IS NULL OR until_on >= first_on),
 vendor text NOT NULL DEFAULT '' CHECK (length(vendor) <= 200),
 basis text NOT NULL CHECK (length(basis) BETWEEN 5 AND 2000),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 approved_by text,
 approved_at timestamptz,
 retired_by text,
 retired_at timestamptz,
 retire_reason text,
 CHECK ((approved_by IS NULL) = (approved_at IS NULL)),
 CHECK ((retired_by IS NULL) = (retired_at IS NULL) AND (retired_at IS NULL) = (retire_reason IS NULL))
);
CREATE INDEX cash_obligations_client ON cash_obligations(organization_id, lower(client_entity), id DESC);
ALTER TABLE cash_obligations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON cash_obligations USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
