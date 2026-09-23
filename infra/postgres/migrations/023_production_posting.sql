BEGIN;
ALTER TABLE quickbooks_connections ADD COLUMN environment text NOT NULL DEFAULT 'sandbox' CHECK(environment IN ('sandbox','production'));
DROP INDEX quickbooks_current_client;
CREATE UNIQUE INDEX quickbooks_current_client ON quickbooks_connections(organization_id,lower(client_entity),environment) WHERE state<>'disconnected';

CREATE TABLE production_posting_grants (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 connection_id bigint NOT NULL REFERENCES quickbooks_connections(id),
 realm text NOT NULL,
 prepaid_account_id text NOT NULL,
 expense_account_id text NOT NULL,
 max_amount numeric(18,2) NOT NULL CHECK(max_amount>0),
 expires_at timestamptz NOT NULL,
 reason text NOT NULL,
 granted_by text NOT NULL,
 granted_at timestamptz NOT NULL DEFAULT now(),
 revoked_by text,
 revoked_at timestamptz,
 CHECK(prepaid_account_id<>expense_account_id)
);
CREATE UNIQUE INDEX production_posting_current_grant ON production_posting_grants(organization_id,lower(client_entity)) WHERE revoked_at IS NULL;

CREATE TABLE production_postings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 workpaper_id bigint NOT NULL UNIQUE REFERENCES workpapers(id),
 source_key text NOT NULL,
 grant_id bigint NOT NULL REFERENCES production_posting_grants(id),
 connection_id bigint NOT NULL REFERENCES quickbooks_connections(id),
 realm text NOT NULL,
 request_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 digest text NOT NULL,
 journal jsonb NOT NULL,
 ledger_before jsonb NOT NULL,
 state text NOT NULL CHECK(state IN ('preview','approved','executing','unknown','verified')),
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 external_id text,
 last_error text NOT NULL DEFAULT '',
 ledger_after jsonb,
 verified_at timestamptz,
 UNIQUE(close_id,source_key)
);
COMMIT;
