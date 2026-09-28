BEGIN;
-- Backend-held sign-in sessions (AUTH_SESSION=server): the browser holds only a random cookie whose SHA-256 is id_hash;
-- WorkOS's tokens are sealed with SECRETS_KEY. Only system work reads the table, before a firm is known.
CREATE TABLE auth_sessions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 id_hash text NOT NULL UNIQUE,
 subject text NOT NULL,
 organization_id text NOT NULL,
 workos_session text NOT NULL,
 email text NOT NULL,
 refresh_token bytea NOT NULL,
 access_token bytea NOT NULL,
 access_expires_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 revoked_at timestamptz
);
CREATE INDEX auth_sessions_expiry ON auth_sessions(expires_at);
ALTER TABLE auth_sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON auth_sessions USING (current_setting('app.organization',true)='*');
COMMIT;
