BEGIN;
-- Open items by file (docs/books-sources.md, Open items and cash dates; delivery step 11): a client whose books are its
-- files brings its unpaid invoices or bills as an export, true at its own as-of time, kept apart from the month's end
-- and from when it was sent. A reviewer confirms how its columns read; the documents read from it are the month's open
-- items, for its aging, open items and collections. The newest file of each side counts.
CREATE TABLE open_item_files (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 side text NOT NULL CHECK (side IN ('receivable','payable')),
 as_of date NOT NULL,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
 digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
 content bytea NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 52428800),
 layout jsonb NOT NULL,
 items jsonb NOT NULL CHECK (jsonb_typeof(items)='array'),
 problems jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(problems)='array'),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX open_item_files_close ON open_item_files(close_id, side, id DESC);
ALTER TABLE open_item_files ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON open_item_files USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
-- A decision about a document of an open-items file is kept as one about a ledger's.
ALTER TABLE open_item_actions DROP CONSTRAINT open_item_actions_source_check,
 ADD CONSTRAINT open_item_actions_source_check CHECK (source IN ('quickbooks','xero','files'));
COMMIT;
