BEGIN;
-- Content grants (docs/admin-panel.md, A4): a staff member's time-boxed access to one firm's client content, so reveals
-- on that firm's screens need no reason each time while it lasts. Each reveal is still recorded in platform_audit,
-- naming the grant; opening and ending a grant are recorded there too.
CREATE TABLE content_grants (
 id bigserial PRIMARY KEY,
 staff_user text NOT NULL CHECK (staff_user <> ''),
 organization_id text NOT NULL REFERENCES organizations(id),
 reason text NOT NULL CHECK (reason <> ''),
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL,
 ended_at timestamptz,
 CHECK (expires_at > created_at AND expires_at <= created_at + interval '8 hours'),
 CHECK (ended_at IS NULL OR ended_at >= created_at)
);
CREATE INDEX content_grants_open ON content_grants(staff_user, organization_id) WHERE ended_at IS NULL;
ALTER TABLE content_grants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON content_grants USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
