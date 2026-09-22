BEGIN;
CREATE TABLE close_scopes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 scope jsonb NOT NULL,
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX close_scopes_latest ON close_scopes(close_id,id DESC);
CREATE TABLE close_signoffs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 decision text NOT NULL CHECK(decision IN ('signed_off','reopened')),
 snapshot_digest text NOT NULL,
 snapshot jsonb NOT NULL,
 note text NOT NULL CHECK(length(note) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX close_signoffs_latest ON close_signoffs(close_id,id DESC);
COMMIT;
