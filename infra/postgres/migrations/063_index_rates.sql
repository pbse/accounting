BEGIN;
-- Published reference rates a preparer looked up, so a loan's rate from an index is checked against the published value.
-- This is shared public data, not a firm's: every firm may read and record it.
CREATE TABLE index_rates (
 name text NOT NULL CHECK (name IN ('SOFR','EFFR')),
 effective_date date NOT NULL,
 percent numeric(7,4) NOT NULL,
 fetched_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (name,effective_date)
);
ALTER TABLE index_rates ENABLE ROW LEVEL SECURITY;
CREATE POLICY shared ON index_rates USING (true) WITH CHECK (true);
COMMIT;
