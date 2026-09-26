BEGIN;
-- An unapplied customer payment applied to an open invoice, or a vendor credit applied to an open bill, under an
-- accountant's rule: previewed, approved by a person, attempted once and read back, like other native corrections.
CREATE TABLE credit_applications (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 kind text NOT NULL CHECK (kind IN ('payment','vendor_credit')),
 capture_id bigint NOT NULL REFERENCES quickbooks_entity_syncs(id),
 realm text NOT NULL,
 source_id text NOT NULL,
 target_id text NOT NULL,
 amount numeric(14,2) NOT NULL CHECK (amount > 0),
 txn_date date,
 bank_account_id text,
 reason text NOT NULL,
 rule_reference text NOT NULL,
 before_source jsonb NOT NULL,
 before_target jsonb NOT NULL,
 request_id uuid NOT NULL DEFAULT gen_random_uuid(),
 digest text NOT NULL,
 state text NOT NULL CHECK (state IN ('preview','approved','executing','unknown','verified')),
 proposed_by text NOT NULL,
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 external_id text,
 last_error text NOT NULL DEFAULT '',
 readback jsonb,
 verified_at timestamptz,
 grant_id bigint REFERENCES production_posting_grants(id),
 UNIQUE (realm,kind,source_id,target_id)
);
CREATE INDEX credit_applications_close ON credit_applications(close_id,state);
ALTER TABLE credit_applications ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON credit_applications USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
ALTER TABLE production_posting_grants DROP CONSTRAINT production_posting_grants_kinds;
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_kinds CHECK (cardinality(kinds) >= 1 AND kinds <@ ARRAY['prepaid','accrual','loan','owner','settlement','bill_correction','vendor_credit','purchase_correction','deposit_correction','payment_application','credit_application']);
COMMIT;
