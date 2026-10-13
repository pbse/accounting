BEGIN;
-- A recorded sign-off's standing as the worker last rechecked it (services/backend/internal/transport/signoff_checks.go).
-- The lists that show many closes at once read it instead of rechecking each close's readiness on every read. There is
-- one row per close, for the sign-off it checked.
CREATE TABLE signoff_checks (
 close_id bigint PRIMARY KEY REFERENCES client_closes(id) ON DELETE CASCADE,
 signoff_id bigint NOT NULL REFERENCES close_signoffs(id) ON DELETE CASCADE,
 state text NOT NULL CHECK (state IN ('signed_off','reopen_required','unknown')),
 checked_at timestamptz NOT NULL
);
ALTER TABLE signoff_checks ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON signoff_checks USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
