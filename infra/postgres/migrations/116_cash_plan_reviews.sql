BEGIN;
-- A cash plan a person reviewed, frozen with the assumptions they accepted. Rows are only ever added; a changed input
-- leaves the review in place and shows it is no longer current.
CREATE TABLE cash_plan_reviews (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 scenario_id bigint REFERENCES cash_scenarios(id),
 company text NOT NULL CHECK (char_length(company) BETWEEN 1 AND 300),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 plan jsonb NOT NULL,
 digest text NOT NULL,
 assumptions text NOT NULL CHECK (char_length(assumptions) BETWEEN 5 AND 4000),
 reviewed_by text NOT NULL,
 reviewed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cash_plan_reviews_close ON cash_plan_reviews(close_id,id);
ALTER TABLE cash_plan_reviews ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON cash_plan_reviews FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON cash_plan_reviews FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
