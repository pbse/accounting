BEGIN;
-- Each server-held session says whether it is a staff session (docs/admin-panel.md). Staff are now the people with a
-- verified address at STAFF_EMAIL_DOMAIN, whose sessions may be in a firm's organization or in none, so the session's
-- organization no longer tells the two apart. Every session so far is a firm session; a staff session from the older
-- platform staff organization signs in again.
ALTER TABLE auth_sessions ADD COLUMN staff boolean NOT NULL DEFAULT false;
COMMIT;
