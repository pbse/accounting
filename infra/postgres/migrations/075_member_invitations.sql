BEGIN;
-- An administrator's email invitation into the firm. The app enrolls the invitee only once WorkOS shows this invitation
-- accepted by that user; sending reserves the address so two sends cannot both email it.
CREATE TABLE member_invitations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 email text NOT NULL CHECK (email=lower(email) AND length(email) BETWEEN 3 AND 254),
 role_slug text NOT NULL,
 role_name text NOT NULL,
 workos_id text UNIQUE,
 state text NOT NULL CHECK (state IN ('sending','pending','accepted','revoked','expired')),
 invited_by text NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 10 AND 2000),
 subject text,
 expires_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 decided_at timestamptz,
 CHECK (state IN ('sending','revoked') OR workos_id IS NOT NULL)
);
CREATE UNIQUE INDEX member_invitation_open ON member_invitations(organization_id,email) WHERE state IN ('sending','pending');
ALTER TABLE member_invitations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON member_invitations USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
