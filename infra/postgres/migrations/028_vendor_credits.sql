BEGIN;
CREATE TABLE vendor_credit_postings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 evidence_id bigint NOT NULL REFERENCES evidence(id),
 evidence_digest text NOT NULL,
 review_id text NOT NULL,
 credit_reference text NOT NULL,
 vendor_name text NOT NULL,
 unbooked_confirmed_by text NOT NULL,
 unbooked_confirmed_at timestamptz NOT NULL DEFAULT now(),
 request_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 realm text NOT NULL,
 digest text NOT NULL,
 credit jsonb NOT NULL,
 state text NOT NULL CHECK(state IN ('preview','approved','executing','unknown','verified')),
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 external_id text,
 last_error text NOT NULL DEFAULT '',
 readback jsonb,
 verified_at timestamptz,
 UNIQUE(workpaper_id)
);
CREATE INDEX vendor_credit_attempts ON vendor_credit_postings(close_id,state);
CREATE UNIQUE INDEX vendor_credit_one_reference ON vendor_credit_postings(close_id,lower(credit_reference)) WHERE state<>'preview';
COMMIT;
