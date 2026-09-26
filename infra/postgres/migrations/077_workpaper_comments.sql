BEGIN;
-- A question or comment on one draft, anchored to the whole draft, one of its inputs or a line of a linked source.
-- Open comments on a draft or its predecessors keep it from approval.
CREATE TABLE workpaper_comments (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 anchor text NOT NULL CHECK (anchor ~ '^(general|field:[a-z0-9_]{1,60}|source:[0-9]+:line:[0-9]+)$'),
 anchor_label text NOT NULL CHECK (char_length(anchor_label) BETWEEN 1 AND 300),
 body text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
 author text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 resolved_by text,
 resolved_at timestamptz,
 resolution text CHECK (char_length(resolution) BETWEEN 1 AND 500),
 CHECK ((resolved_at IS NULL) = (resolved_by IS NULL) AND (resolved_at IS NULL) = (resolution IS NULL))
);
CREATE INDEX workpaper_comment_open ON workpaper_comments(workpaper_id) WHERE resolved_at IS NULL;
ALTER TABLE workpaper_comments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workpaper_comments USING (current_setting('app.organization',true)='*' OR workpaper_id IN (SELECT id FROM workpapers));
COMMIT;
