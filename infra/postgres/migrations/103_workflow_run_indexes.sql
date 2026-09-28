-- Reads of the admin panel that grow with a firm's runs:
-- a run's child runs, found by their parent (the run page reads them on every refresh);
-- a firm's active and recently failed runs, counted by state (the firms list and a firm's page);
-- a firm's latest run activity (the same screens).
CREATE INDEX workflow_runs_children ON workflow_runs (parent_run_id, id) WHERE parent_run_id IS NOT NULL;
CREATE INDEX workflow_runs_firm_state ON workflow_runs (organization_id, state, finished_at);
CREATE INDEX workflow_runs_firm_updated ON workflow_runs (organization_id, updated_at);
