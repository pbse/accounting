BEGIN;
CREATE TABLE bill_corrections (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 capture_id bigint NOT NULL REFERENCES quickbooks_entity_syncs(id),
 evidence_id bigint NOT NULL REFERENCES evidence(id),
 realm text NOT NULL,
 bill_id text NOT NULL,
 treatment text NOT NULL CHECK(treatment IN ('uncategorized_expense','inconsistent_coding')),
 reason text NOT NULL,
 target_account_id text NOT NULL,
 target_account_name text NOT NULL,
 before_bill jsonb NOT NULL,
 digest text NOT NULL,
 state text NOT NULL CHECK(state IN ('preview','approved','executing','unknown','verified')),
 proposed_by text NOT NULL,
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 last_error text NOT NULL DEFAULT '',
 readback jsonb,
 verified_at timestamptz,
 UNIQUE(realm,bill_id),
 CONSTRAINT bill_correction_source_scope FOREIGN KEY(close_id,evidence_id) REFERENCES evidence(close_id,id)
);
CREATE INDEX bill_corrections_close ON bill_corrections(close_id,state);
COMMIT;
