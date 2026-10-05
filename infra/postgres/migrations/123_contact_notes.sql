BEGIN;
-- Notes sent through the public contact page (docs/marketing-site.md). They belong to no firm: only platform work
-- ('*') may read or write them. The edge forwards each kept note to our inbox.
CREATE TABLE contact_notes (
 id bigserial PRIMARY KEY,
 name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
 email text NOT NULL CHECK (char_length(email) BETWEEN 3 AND 254),
 firm text NOT NULL CHECK (char_length(firm) BETWEEN 1 AND 160),
 note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 4000),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX contact_notes_recent ON contact_notes(created_at);
ALTER TABLE contact_notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY platform ON contact_notes USING (current_setting('app.organization',true)='*');
COMMIT;
