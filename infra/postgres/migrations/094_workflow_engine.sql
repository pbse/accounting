-- Workflow engine (docs/workflow-engine.md). The only migration the engine needs: new workflows,
-- activities and plans are code or rows, never schema. A NULL organization_id is platform work,
-- visible to system work only.

CREATE TABLE workflow_runs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text REFERENCES organizations(id),
 workflow text NOT NULL CHECK (workflow ~ '^[a-z][a-z0-9_]{1,62}$'),
 code_version text NOT NULL DEFAULT '',
 plan_id bigint,
 parent_run_id bigint REFERENCES workflow_runs(id),
 parent_step text,
 idempotency_key text NOT NULL CHECK (idempotency_key <> '' AND length(idempotency_key) <= 200),
 concurrency_key text CHECK (concurrency_key <> ''),
 state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','waiting','completed','failed','cancelled')),
 input jsonb NOT NULL DEFAULT '{}'::jsonb,
 output jsonb,
 error text NOT NULL DEFAULT '',
 wake_at timestamptz,
 waiting_on text NOT NULL DEFAULT '',
 claim text,
 claimed_at timestamptz,
 attempts integer NOT NULL DEFAULT 0,
 cancel_requested_at timestamptz,
 cancel_reason text NOT NULL DEFAULT '',
 cancelled_by text NOT NULL DEFAULT '',
 started_by text NOT NULL CHECK (started_by <> ''),
 agent text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 finished_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((state IN ('completed','failed','cancelled')) = (finished_at IS NOT NULL)),
 CHECK ((state = 'running') = (claim IS NOT NULL))
);
-- Starting the same (firm, workflow, idempotency key) twice returns the first run.
CREATE UNIQUE INDEX workflow_runs_idempotency ON workflow_runs (coalesce(organization_id,''), workflow, idempotency_key);
-- One active run per concurrency key, e.g. one run working on a close at a time.
CREATE UNIQUE INDEX workflow_runs_concurrency ON workflow_runs (coalesce(organization_id,''), concurrency_key)
 WHERE concurrency_key IS NOT NULL AND state IN ('queued','running','waiting');
CREATE INDEX workflow_runs_runnable ON workflow_runs (wake_at) WHERE state IN ('queued','waiting');
CREATE INDEX workflow_runs_leased ON workflow_runs (claimed_at) WHERE state = 'running';
CREATE INDEX workflow_runs_waiting_on ON workflow_runs (waiting_on) WHERE state = 'waiting';
CREATE INDEX workflow_runs_listing ON workflow_runs (organization_id, workflow, created_at DESC);

CREATE TABLE workflow_steps (
 run_id bigint NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
 key text NOT NULL CHECK (key <> '' AND length(key) <= 200),
 organization_id text REFERENCES organizations(id),
 seq integer NOT NULL,
 kind text NOT NULL CHECK (kind IN ('step','effect','activity','sleep','event','approval','child','value')),
 state text NOT NULL CHECK (state IN ('running','waiting','completed','failed','unknown','cancelled')),
 attempts integer NOT NULL DEFAULT 0,
 retry_at timestamptz,
 wake_at timestamptz,
 input jsonb,
 output jsonb,
 error text NOT NULL DEFAULT '',
 waiting_on text NOT NULL DEFAULT '',
 child_run_id bigint REFERENCES workflow_runs(id),
 actor text NOT NULL DEFAULT '',
 note text NOT NULL DEFAULT '',
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 PRIMARY KEY (run_id, key),
 UNIQUE (run_id, seq)
);

-- Append-only signals. A wait consumes the earliest matching event recorded after its run started.
CREATE TABLE workflow_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text REFERENCES organizations(id),
 key text NOT NULL CHECK (key <> '' AND length(key) <= 200),
 payload jsonb NOT NULL DEFAULT '{}'::jsonb,
 actor text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workflow_events_key ON workflow_events (key, created_at);

-- Staff-built and agent-proposed plans. An approval binds the exact digest; only an approved plan
-- can be linked to a run, and nothing can approve without a recorded approver.
CREATE TABLE workflow_plans (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text REFERENCES organizations(id),
 title text NOT NULL CHECK (title <> ''),
 plan jsonb NOT NULL,
 digest text NOT NULL,
 state text NOT NULL DEFAULT 'draft' CHECK (state IN ('draft','proposed','approved','rejected','archived')),
 source text NOT NULL CHECK (source IN ('staff','agent','member')),
 rationale text NOT NULL DEFAULT '',
 agent text NOT NULL DEFAULT '',
 created_by text NOT NULL CHECK (created_by <> ''),
 approved_by text,
 approved_at timestamptz,
 approved_digest text,
 is_template boolean NOT NULL DEFAULT false,
 template_id bigint REFERENCES workflow_plans(id),
 run_id bigint REFERENCES workflow_runs(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (state <> 'approved' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL AND approved_digest = digest)),
 CHECK (run_id IS NULL OR state = 'approved'),
 CHECK (source <> 'agent' OR state <> 'draft')
);
ALTER TABLE workflow_runs ADD FOREIGN KEY (plan_id) REFERENCES workflow_plans(id);

ALTER TABLE workflow_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workflow_runs USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE workflow_steps ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workflow_steps USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE workflow_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workflow_events USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE workflow_plans ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workflow_plans USING (current_setting('app.organization',true) IN ('*',organization_id));
