BEGIN;
CREATE TABLE IF NOT EXISTS sandbox_postings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 workpaper_id bigint NOT NULL UNIQUE REFERENCES workpapers(id),
 source_key text NOT NULL,
 request_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 realm text NOT NULL,
 digest text NOT NULL,
 journal jsonb NOT NULL,
 state text NOT NULL CHECK(state IN ('preview','approved','executing','unknown','verified')),
 approved_by text,
 approval_expires_at timestamptz,
 attempted_by text,
 attempted_at timestamptz,
 external_id text,
 last_error text NOT NULL DEFAULT '',
 verified_at timestamptz,
 UNIQUE(close_id,source_key)
);
COMMIT;
