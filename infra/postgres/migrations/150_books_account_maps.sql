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
 -- accounts is the whole map: each account's key (its number, else its name) to its class, type, name and number, and
 -- the files whose readings it was confirmed on.
 accounts jsonb NOT NULL CHECK (jsonb_typeof(accounts)='object'),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_account_maps_client ON books_account_maps(organization_id,lower(client_entity),id DESC);
ALTER TABLE books_account_maps ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_account_maps FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_account_maps FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_change ON books_account_maps FOR UPDATE USING (current_setting('app.organization',true) IN ('*',organization_id)) WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

-- books_forget_file_accounts removes from org's maps what only the files ids named: an account confirmed on no other
-- file goes from every version, and the files leave the rest, so a file deleted, or removed with its month, leaves
-- nothing of its content in a map.
CREATE FUNCTION books_forget_file_accounts(org text, ids text[]) RETURNS void LANGUAGE sql AS $$
 UPDATE books_account_maps m SET accounts=COALESCE((SELECT jsonb_object_agg(e.key,jsonb_set(e.value,'{files}',COALESCE(e.value->'files','[]'::jsonb)-ids))
   FROM jsonb_each(m.accounts) e WHERE jsonb_array_length(COALESCE(e.value->'files','[]'::jsonb)-ids)>0),'{}'::jsonb)
 WHERE m.organization_id=org AND EXISTS(SELECT 1 FROM jsonb_each(m.accounts) e WHERE COALESCE(e.value->'files','[]'::jsonb) ?| ids)
$$;
COMMIT;
