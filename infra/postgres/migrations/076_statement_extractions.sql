BEGIN;
-- A model's proposed rows for one PDF statement, and the rows a reviewer confirmed from it into CSV evidence.
CREATE TABLE statement_extractions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 source_id bigint NOT NULL REFERENCES evidence(id),
 source_digest text NOT NULL,
 proposal jsonb NOT NULL,
 model text NOT NULL,
 response_id text NOT NULL DEFAULT '',
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 confirmed jsonb,
 confirmed_by text,
 confirmed_at timestamptz,
 produced_id bigint REFERENCES evidence(id),
 CHECK ((confirmed IS NULL) = (produced_id IS NULL) AND (confirmed IS NULL) = (confirmed_at IS NULL) AND (confirmed IS NULL) = (confirmed_by IS NULL))
);
CREATE INDEX statement_extraction_source ON statement_extractions(source_id,id);
ALTER TABLE statement_extractions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON statement_extractions USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
