BEGIN;
-- A member's own settings in a firm (Your settings, docs/design-system.md): today only their time zone, as an IANA name
-- the API checks before it is kept. Their name and email stay in WorkOS; their notification kinds stay in
-- attention_preferences. A row leaves with the membership, as attention_preferences does.
CREATE TABLE member_settings (
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL,
 time_zone text NOT NULL DEFAULT '' CHECK (length(time_zone) <= 64),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (organization_id,subject)
);
ALTER TABLE member_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON member_settings USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
