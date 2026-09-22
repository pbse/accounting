BEGIN;
CREATE TABLE coverage_bindings (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 snapshot_id bigint NOT NULL REFERENCES quickbooks_snapshots(id) ON DELETE CASCADE,
 account_id text NOT NULL,
 job_ids jsonb NOT NULL CHECK(jsonb_typeof(job_ids)='array'),
 excluded boolean NOT NULL DEFAULT false,
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX coverage_bindings_current ON coverage_bindings(close_id,snapshot_id,account_id,id DESC);
ALTER TABLE work_requests ALTER COLUMN job_id DROP NOT NULL;
ALTER TABLE work_requests ADD COLUMN coverage_scope jsonb;
CREATE TABLE work_request_targets (
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 job_id bigint NOT NULL REFERENCES preparation_intakes(job_id) ON DELETE CASCADE,
 revision integer NOT NULL,
 PRIMARY KEY(request_id,job_id)
);
CREATE VIEW request_jobs AS
 SELECT id AS request_id,job_id FROM work_requests WHERE job_id IS NOT NULL
 UNION SELECT request_id,job_id FROM work_request_targets;
CREATE TABLE client_attachments (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 name text NOT NULL,
 digest text NOT NULL,
 body bytea NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 5242880),
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','scanning','clean','infected','failed','released','rejected')),
 claim text,
 started_at timestamptz,
 scanned_at timestamptz,
 scan_version text NOT NULL DEFAULT '',
 scanned_digest text NOT NULL DEFAULT '',
 error text NOT NULL DEFAULT '',
 source_id bigint REFERENCES evidence(id),
 decided_by text,
 decision_note text,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(request_id,digest)
);
COMMIT;
