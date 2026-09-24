BEGIN;
CREATE TABLE close_effort (
 close_id bigint NOT NULL REFERENCES client_closes(id),
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
 minute timestamptz NOT NULL,
 activity text NOT NULL CHECK (activity IN ('prepare','review','evidence','requests','signoff','configuration')),
 seconds integer NOT NULL CHECK (seconds BETWEEN 1 AND 60),
 PRIMARY KEY (close_id, actor, minute)
);
CREATE TABLE close_effort_entries (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
 activity text NOT NULL CHECK (activity IN ('prepare','review','evidence','requests','signoff','configuration')),
 minutes integer NOT NULL CHECK (minutes BETWEEN 1 AND 480),
 note text NOT NULL CHECK (length(note) BETWEEN 5 AND 500),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX close_effort_entries_close ON close_effort_entries(close_id);
COMMIT;
