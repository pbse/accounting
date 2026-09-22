BEGIN;
CREATE TABLE evidence (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 name text NOT NULL,
 digest text NOT NULL,
 content text NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 524288),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(close_id,digest)
);
CREATE TABLE preparation_plans (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 digest text NOT NULL,
 plan jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(close_id,digest)
);
CREATE TABLE preparation_jobs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 digest text NOT NULL,
 plans jsonb NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','completed','failed','cancelled')),
 workpaper_ids jsonb NOT NULL DEFAULT '[]',
 error text NOT NULL DEFAULT '',
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 UNIQUE(close_id,digest)
);
CREATE INDEX preparation_jobs_pending ON preparation_jobs(id) WHERE state='queued';
COMMIT;
