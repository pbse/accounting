BEGIN;
-- A document of a delegated close that a person handed to a teammate to investigate: who has it and why. via_grant says
-- the teammate's access was an explicit client assignment, rechecked by the agent; document and assignee_name are
-- what was shown when it was handed. The agent closes it once the document no longer needs a person.
CREATE TABLE goal_handoffs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 goal_id bigint NOT NULL REFERENCES close_goals(id) ON DELETE CASCADE,
 job_id bigint NOT NULL REFERENCES preparation_jobs(id) ON DELETE CASCADE,
 document text NOT NULL,
 assignee text NOT NULL,
 assignee_name text NOT NULL,
 via_grant boolean NOT NULL,
 note text NOT NULL DEFAULT '' CHECK (length(note)<=500),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 closed_at timestamptz,
 closed_by text,
 CHECK ((closed_at IS NULL)=(closed_by IS NULL))
);
CREATE UNIQUE INDEX goal_handoff_open ON goal_handoffs(job_id) WHERE closed_at IS NULL;
ALTER TABLE goal_handoffs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON goal_handoffs USING (current_setting('app.organization',true)='*' OR goal_id IN (SELECT id FROM close_goals));
COMMIT;
