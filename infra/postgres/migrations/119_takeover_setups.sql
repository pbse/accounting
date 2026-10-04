BEGIN;
-- A confirmed takeover setup of a client's opening month: the company, basis, procedures, access, account scope and
-- monthly hand-over as the reviewer saw them, with the client's timezone. Rows are only ever added.
CREATE TABLE takeover_setups (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 digest text NOT NULL,
 content jsonb NOT NULL,
 timezone text NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 64),
 confirmed_by text NOT NULL,
 confirmed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX takeover_setups_close ON takeover_setups(close_id,id);
ALTER TABLE takeover_setups ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON takeover_setups FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON takeover_setups FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
