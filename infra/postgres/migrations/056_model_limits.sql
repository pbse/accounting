BEGIN;
-- A firm-wide daily cap on model calls, counted per UTC day.
ALTER TABLE organizations ADD COLUMN max_model_calls_per_day integer NOT NULL DEFAULT 2000 CHECK (max_model_calls_per_day BETWEEN 1 AND 100000);
CREATE TABLE model_calls (
 organization_id text NOT NULL REFERENCES organizations(id),
 day date NOT NULL,
 calls integer NOT NULL CHECK (calls >= 0),
 PRIMARY KEY (organization_id, day)
);
ALTER TABLE model_calls ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON model_calls USING (current_setting('app.organization',true) IN ('*',organization_id));
-- A job refused by the limit waits until then, so it cannot hold up other firms' jobs.
ALTER TABLE preparation_intakes ADD COLUMN agent_retry_at timestamptz;

-- The last scheduled Xero capture attempt for a close: one claim, at most one attempt a day.
ALTER TABLE client_closes ADD COLUMN xero_refresh_at timestamptz;

-- Each replay of confirmed readings, kept so a model change can be compared later.
CREATE TABLE replay_runs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 model text NOT NULL,
 actor text NOT NULL,
 items jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX replay_runs_model ON replay_runs(organization_id, model);
ALTER TABLE replay_runs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON replay_runs USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
