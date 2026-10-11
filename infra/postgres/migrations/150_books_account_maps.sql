BEGIN;
-- Account maps (docs/books-sources.md, How an upload becomes the month's books, step 4; delivery step 6): a client's
-- accounts as its files name them, each with the class and type the month's books carry for any source, so a month on
-- files reads in the statements and checks as a connected ledger's does. A reviewer confirms the map against a file's
-- confirmed reading; each confirmation is a new version holding the whole map with its reason, so the newest is in
-- force and every earlier one stays on record.
CREATE TABLE books_account_maps (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 -- import_id is the file whose reading the version was confirmed against, and period its month; the month's retention
 -- removes the file, never the client's map.
 import_id bigint REFERENCES books_imports(id) ON DELETE SET NULL,
 period text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 -- accounts is the whole map: each account's key (its number, else its name) to its class, type, name and number.
 accounts jsonb NOT NULL CHECK (jsonb_typeof(accounts)='object'),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_account_maps_client ON books_account_maps(organization_id,lower(client_entity),id DESC);
ALTER TABLE books_account_maps ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_account_maps FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_account_maps FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
