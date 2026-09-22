BEGIN;
ALTER TABLE preparation_jobs DROP CONSTRAINT preparation_jobs_state_check;
ALTER TABLE preparation_jobs ADD CHECK(state IN ('waiting_evidence','interpreting','needs_confirmation','waiting_client','queued','completed','failed','cancelled'));
ALTER TABLE preparation_intakes ADD COLUMN agent_claim text;
ALTER TABLE preparation_intakes ADD COLUMN agent_started_at timestamptz;
ALTER TABLE preparation_intakes ADD COLUMN agent_attempts integer NOT NULL DEFAULT 0;
CREATE TABLE preparation_answers (
 job_id bigint NOT NULL REFERENCES preparation_intakes(job_id) ON DELETE CASCADE,
 source_id bigint NOT NULL REFERENCES evidence(id),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(job_id,source_id)
);
COMMIT;
