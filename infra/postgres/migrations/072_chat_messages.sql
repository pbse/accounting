BEGIN;
-- A member's assistant conversation for one close (close_id) or firm-wide (NULL). Only its author reads it.
CREATE TABLE chat_messages (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 author text NOT NULL,
 close_id bigint REFERENCES client_closes(id),
 workpaper_id bigint REFERENCES workpapers(id),
 role text NOT NULL CHECK (role IN ('user','assistant')),
 body text NOT NULL CHECK (length(body) BETWEEN 1 AND 8000),
 citations jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(citations)='array'),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK (close_id IS NOT NULL OR workpaper_id IS NULL)
);
CREATE INDEX chat_conversation ON chat_messages(organization_id,author,close_id,id);
ALTER TABLE chat_messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON chat_messages USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
