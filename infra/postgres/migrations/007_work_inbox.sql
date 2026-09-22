BEGIN;
ALTER TABLE preparation_jobs DROP CONSTRAINT preparation_jobs_state_check;
ALTER TABLE preparation_jobs ADD CHECK(state IN ('waiting_evidence','needs_confirmation','waiting_client','queued','completed','failed','cancelled'));
CREATE TABLE preparation_intakes (
 job_id bigint PRIMARY KEY REFERENCES preparation_jobs(id) ON DELETE CASCADE,
 source_id bigint NOT NULL UNIQUE REFERENCES evidence(id),
 revision integer NOT NULL DEFAULT 0,
 proposal jsonb NOT NULL DEFAULT '{}',
 confirmed_by text,
 confirmed_at timestamptz
);
CREATE TABLE work_requests (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 job_id bigint NOT NULL REFERENCES preparation_intakes(job_id) ON DELETE CASCADE,
 draft_id bigint NOT NULL REFERENCES request_drafts(id),
 revision integer NOT NULL,
 recipient text NOT NULL,
 approved_by text NOT NULL,
 approved_at timestamptz NOT NULL DEFAULT now(),
 delivered_by text,
 delivery_reference text,
 delivered_at timestamptz,
 UNIQUE(job_id,revision)
);
CREATE TABLE work_replies (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 source_id bigint NOT NULL REFERENCES evidence(id),
 recorded_by text NOT NULL,
 received_from text NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(request_id,source_id)
);
CREATE INDEX preparation_intakes_pending ON preparation_jobs(id) WHERE state='waiting_evidence';
COMMIT;
