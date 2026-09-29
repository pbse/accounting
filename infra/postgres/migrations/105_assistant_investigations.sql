BEGIN;
-- Whether the assistant may read a close's own records through the close-scoped read tools before writing an answer
-- (an investigation), separately from written answers, and the trace of what each attempt read.
ALTER TABLE organizations ADD COLUMN assistant_investigations boolean NOT NULL DEFAULT false;
ALTER TABLE organization_policy_changes ADD COLUMN assistant_investigations boolean;
ALTER TABLE assistant_answers ADD COLUMN trace jsonb;
COMMIT;
