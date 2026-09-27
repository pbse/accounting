BEGIN;
-- A client's monthly hand-over: when a month ends, the agent opens that month's close if needed and hands it to the
-- agent with the finish line and due offset (days after month end) its creator approved. via_grant says the creator's
-- access was an explicit client assignment, rechecked each month; last_period is the latest month handled; waiting
-- says why the next month could not be handled yet; checked_at orders the worker's attempts, least recent first.
CREATE TABLE close_routines (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 author text NOT NULL,
 via_grant boolean NOT NULL,
 finish text NOT NULL CHECK (finish IN ('review','signoff')),
 due_days integer CHECK (due_days BETWEEN 1 AND 60),
 last_period text NOT NULL CHECK (last_period ~ '^[0-9]{4}-[0-9]{2}$'),
 waiting text NOT NULL DEFAULT '',
 checked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 stopped_at timestamptz,
 stopped_by text,
 stop_reason text,
 CHECK ((stopped_at IS NULL)=(stopped_by IS NULL))
);
CREATE UNIQUE INDEX close_routine_active ON close_routines(organization_id,lower(client_entity)) WHERE stopped_at IS NULL;
ALTER TABLE close_routines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON close_routines USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE close_goals ADD COLUMN routine_id bigint REFERENCES close_routines(id);
CREATE UNIQUE INDEX close_goal_routine ON close_goals(close_id,routine_id) WHERE routine_id IS NOT NULL;
COMMIT;
