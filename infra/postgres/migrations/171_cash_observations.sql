BEGIN;
-- Current cash on files (docs/books-sources.md, Open items and cash dates; delivery step 11): a client whose books are
-- its files brings its cash at a recent date. A reviewer records it as book cash for each bank account, read from a
-- Trial Balance or cash balances run to that date, or as each account's bank balance with the outstanding items that
-- bridge it to book cash (a deposit in transit adds, an uncleared check subtracts), each with its evidence; note says
-- where it comes from. It opens a files month's cash plan from its date; it is never a month-end reconciliation.
CREATE TABLE cash_observations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 -- books_version is the month's books it was recorded against: replaced, they leave it behind.
 books_version text NOT NULL,
 as_of date NOT NULL,
 kind text NOT NULL CHECK (kind IN ('book','bank')),
 accounts jsonb NOT NULL CHECK (jsonb_typeof(accounts)='array' AND jsonb_array_length(accounts) BETWEEN 1 AND 50),
 bridge jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(bridge)='array' AND jsonb_array_length(bridge) <= 200 AND (kind='bank' OR bridge='[]'::jsonb)),
 book_cash numeric NOT NULL,
 note text NOT NULL CHECK (length(note) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cash_observations_close ON cash_observations(close_id, id DESC);
ALTER TABLE cash_observations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON cash_observations USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
