BEGIN;
-- An approval request asks a person to review a draft; it never changes review state.
CREATE TABLE approval_requests (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 requested_by text NOT NULL,
 note text NOT NULL CHECK (length(note) BETWEEN 5 AND 1000),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approval_requests_paper ON approval_requests(workpaper_id,id DESC);
COMMIT;
