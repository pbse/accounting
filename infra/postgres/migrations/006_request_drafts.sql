BEGIN;
CREATE TABLE request_drafts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 body text NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 20000),
 workpaper_ids jsonb NOT NULL,
 context_digest text NOT NULL,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX request_drafts_close ON request_drafts(close_id,id DESC);
COMMIT;
