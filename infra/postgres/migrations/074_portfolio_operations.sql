BEGIN;
-- A member's portfolio goal: the closes the server selected for one action, each with the digest it was previewed at and its result.
CREATE TABLE portfolio_operations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 author text NOT NULL,
 action text NOT NULL CHECK (action IN ('prepare','recheck','stop')),
 goal text NOT NULL CHECK (length(goal) BETWEEN 1 AND 300),
 scope text NOT NULL,
 has_more boolean NOT NULL,
 reason text CHECK (length(reason) BETWEEN 1 AND 500),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX portfolio_author ON portfolio_operations(organization_id,author,id);
CREATE TABLE portfolio_items (
 operation_id bigint NOT NULL REFERENCES portfolio_operations(id) ON DELETE CASCADE,
 position integer NOT NULL,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 label text NOT NULL,
 digest text NOT NULL,
 count integer NOT NULL CHECK (count>=0),
 state text NOT NULL CHECK (state IN ('pending','done','failed','skipped')),
 result text NOT NULL DEFAULT '',
 finished_at timestamptz,
 PRIMARY KEY (operation_id,close_id)
);
CREATE INDEX portfolio_item_close ON portfolio_items(close_id);
ALTER TABLE portfolio_operations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON portfolio_operations USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE portfolio_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON portfolio_items USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
