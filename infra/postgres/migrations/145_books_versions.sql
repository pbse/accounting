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
ALTER TABLE books_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON books_versions USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE books_lines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON books_lines USING (current_setting('app.organization',true)='*' OR version_id IN (SELECT id FROM books_versions));
COMMIT;
