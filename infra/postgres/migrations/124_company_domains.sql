BEGIN;
-- A firm created from a company email domain at first sign-in (docs/sign-in.md). Later people with a verified address at
-- the domain join that firm, so a domain belongs to one firm at most. Firms set up by hand have none.
ALTER TABLE organizations ADD COLUMN email_domain text UNIQUE
 CHECK (email_domain = lower(email_domain) AND char_length(email_domain) <= 253 AND email_domain ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$');
-- Each role a firm's administrator gives a member, with the role before and why. WorkOS holds the role itself.
CREATE TABLE member_role_changes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL CHECK (left(subject,5)='user_' AND length(subject) BETWEEN 6 AND 200),
 from_role text NOT NULL CHECK (char_length(from_role) BETWEEN 1 AND 100),
 to_role text NOT NULL CHECK (char_length(to_role) BETWEEN 1 AND 100),
 reason text NOT NULL CHECK (char_length(reason) BETWEEN 10 AND 2000),
 actor text NOT NULL CHECK (left(actor,5)='user_'),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX member_role_changes_recent ON member_role_changes(organization_id,id DESC);
ALTER TABLE member_role_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON member_role_changes USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
