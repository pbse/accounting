BEGIN;
-- Notes sent through the public contact page (docs/marketing-site.md). They belong to no firm: only platform work
-- ('*') may read or write them. The edge forwards each kept note to our inbox. submission is the page's key for one
-- note, so a retry finds the note it already kept; sender_digest is a one-way digest of the network address it came
-- from, for the per-address limit, cleared once the note is an hour old.
CREATE TABLE contact_notes (
 id bigserial PRIMARY KEY,
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 email text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
 firm text NOT NULL CHECK (char_length(firm) BETWEEN 1 AND 160),
 note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 4000),
 submission text UNIQUE CHECK (submission ~ '^[A-Za-z0-9-]{16,64}$'),
 sender_digest text CHECK (sender_digest ~ '^[0-9a-f]{64}$'),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contact_notes_recent ON contact_notes(created_at);
CREATE INDEX contact_notes_sender ON contact_notes(sender_digest, created_at) WHERE sender_digest IS NOT NULL;
ALTER TABLE contact_notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY platform ON contact_notes USING (current_setting('app.organization',true)='*');
COMMIT;
