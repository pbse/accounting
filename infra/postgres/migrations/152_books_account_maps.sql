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
 -- the confirmed readings (books_layouts ids) it was confirmed on.
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

-- books_forget_readings removes from client name's maps what only the readings ids (books_layouts ids, of files being
-- deleted) named: an account confirmed on no other reading goes from every version, and the readings leave the rest, so
-- a file deleted, or removed with its month, leaves nothing of its content in a map. It takes the client's map lock
-- first, as a confirmation does, so none under way carries the file's accounts past it.
CREATE FUNCTION books_forget_readings(org text, name text, ids text[]) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtext('books_account_maps:'||org||':'||client_identity(org,name)));
 UPDATE books_account_maps m SET accounts=COALESCE((SELECT jsonb_object_agg(e.key,jsonb_set(e.value,'{readings}',COALESCE(e.value->'readings','[]'::jsonb)-ids))
   FROM jsonb_each(m.accounts) e WHERE jsonb_array_length(COALESCE(e.value->'readings','[]'::jsonb)-ids)>0),'{}'::jsonb)
 WHERE m.organization_id=org AND client_identity(m.organization_id,m.client_entity)=client_identity(org,name)
  AND EXISTS(SELECT 1 FROM jsonb_each(m.accounts) e WHERE COALESCE(e.value->'readings','[]'::jsonb) ?| ids);
END $$;
COMMIT;
