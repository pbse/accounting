BEGIN;
-- Who held a close and who changed that (B02): a person taking an unheld close or releasing their own, the holder handing
-- it to a teammate, or a firm administrator reassigning it. from_owner and to_owner are subjects, '' for nobody; reason
-- is the note given with an assignment.
CREATE TABLE close_owner_changes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 from_owner text NOT NULL CHECK (length(from_owner)<=200),
 to_owner text NOT NULL CHECK (length(to_owner)<=200),
 actor text NOT NULL,
 reason text NOT NULL DEFAULT '' CHECK (length(reason)<=500),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK (from_owner<>to_owner)
);
CREATE INDEX close_owner_changes_close ON close_owner_changes(close_id,id DESC);
ALTER TABLE close_owner_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON close_owner_changes USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
