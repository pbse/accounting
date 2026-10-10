BEGIN;
-- One month-books record every source writes (docs/books-sources.md, One month-books record): a month's books as the
-- month-end readers read them (readLedgerMonth), stored with lineage, whichever source they came from. QuickBooks and
-- Xero captures keep their raw payloads, and the worker writes a version here for each month-end capture, idempotently;
-- the readers do not switch to it until a parity check agrees for every close (delivery step 5).
CREATE TABLE books_versions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 period text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 -- source is quickbooks, xero or files; source_id the source's own record (a capture's id, or an import's).
 source text NOT NULL CHECK (source IN ('quickbooks','xero','files')),
 source_id text NOT NULL,
 -- company is the ledger company the books came from (a QuickBooks realm, a Xero tenant).
 company text NOT NULL DEFAULT '',
 as_of date NOT NULL,
 captured_at timestamptz NOT NULL,
 currency text NOT NULL DEFAULT '',
 basis text NOT NULL DEFAULT 'accrual' CHECK (basis IN ('accrual','cash','modified_cash')),
 -- typed says the accounts carry the ledger's own types (QuickBooks); Xero's carry classes only.
 typed boolean NOT NULL,
 -- accounts: {id: {name,class,type,subtype}}; balances (debit minus credit) and activity (profit and loss presentation
 -- sign), {id: decimal string}; cash_flow: the ledger's own statement when the source has one; uncounted: Xero bank
 -- accounts that are neither cash nor credit cards.
 accounts jsonb NOT NULL,
 balances jsonb NOT NULL,
 activity jsonb NOT NULL,
 cash_flow jsonb,
 uncounted jsonb NOT NULL DEFAULT '[]'::jsonb,
 digest text NOT NULL CHECK (length(digest)=64),
 -- lines_digest is the digest of its ledger lines (empty without any), so the parity check compares them too.
 lines_digest text NOT NULL DEFAULT '',
 -- unreadable is why the readers cannot read the capture (its figures are then empty); lines_unreadable why its ledger
 -- lines could not be kept. A version is written once, with either.
 unreadable text NOT NULL DEFAULT '',
 lines_unreadable text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (source,source_id)
);
CREATE INDEX books_versions_close ON books_versions(close_id,captured_at DESC,id DESC);
-- books_lines: ledger lines when the source has them, each with the location it came from.
CREATE TABLE books_lines (
 version_id bigint NOT NULL REFERENCES books_versions(id) ON DELETE CASCADE,
 position integer NOT NULL,
 account_id text NOT NULL,
 location text NOT NULL,
 cells jsonb NOT NULL,
 PRIMARY KEY (version_id,position)
);
-- books_capture_cursor: the last capture of each source the worker has written a version for (captures only ever
-- grow), so each pass reads only the captures after it.
CREATE TABLE books_capture_cursor (
 source text PRIMARY KEY CHECK (source IN ('quickbooks','xero')),
 last_id bigint NOT NULL
);
-- books_parity: each run of the parity check over every version, by the worker: how many it checked, and the captures
-- whose stored version no longer matches what the readers compute (identifiers only). A run in progress has no
-- finished_at. Staff read the latest finished run.
CREATE TABLE books_parity (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 checked integer NOT NULL DEFAULT 0,
 -- pending is how many month-end captures had no version yet when the run began: a run is the gate only when none had.
 pending integer NOT NULL DEFAULT 0,
 mismatched jsonb NOT NULL DEFAULT '[]'::jsonb
);
ALTER TABLE books_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON books_versions USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE books_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON books_lines USING (current_setting('app.organization',true)='*' OR version_id IN (SELECT id FROM books_versions));
ALTER TABLE books_parity ENABLE ROW LEVEL SECURITY;
ALTER TABLE books_capture_cursor ENABLE ROW LEVEL SECURITY;
CREATE POLICY platform ON books_capture_cursor USING (current_setting('app.organization',true)='*');
CREATE POLICY platform ON books_parity USING (current_setting('app.organization',true)='*');
COMMIT;
