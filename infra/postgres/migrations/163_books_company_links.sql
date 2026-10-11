BEGIN;
-- Cross-source identity (docs/books-sources.md, History and takeover; delivery step 7): a reviewer confirms once that a
-- client's books on files and its books on a live ledger company are the same company, mapping each of the file's
-- accounts to the ledger's, so months on the two sources can be compared. Each confirmation is a new version holding
-- the whole mapping with its reason; the newest for the company is in force, and every earlier one stays on record.
CREATE TABLE books_company_links (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 ledger text NOT NULL CHECK (ledger IN ('quickbooks','xero')),
 -- company is the ledger's own company: a QuickBooks realm or a Xero tenant.
 company text NOT NULL CHECK (length(company) BETWEEN 1 AND 200),
 -- accounts maps each file account key the client's account map holds to the ledger's account id; map_stamp is the
 -- account map it was confirmed on: once the map changes, the link waits to be reviewed again.
 accounts jsonb NOT NULL CHECK (jsonb_typeof(accounts)='object'),
 map_stamp text NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_company_links_client ON books_company_links(organization_id,lower(client_entity),ledger,company,id DESC);
ALTER TABLE books_company_links ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_company_links FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_company_links FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_change ON books_company_links FOR UPDATE USING (current_setting('app.organization',true) IN ('*',organization_id)) WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

-- books_forget_readings, as before, now also forgets from the client's company links the accounts no map holds.
CREATE OR REPLACE FUNCTION books_forget_readings(org text, name text, ids text[]) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtext('books_account_maps:'||org||':'||client_identity(org,name)));
 UPDATE books_account_maps m SET accounts=COALESCE((SELECT jsonb_object_agg(e.key,jsonb_set(e.value,'{readings}',COALESCE(e.value->'readings','[]'::jsonb)-ids))
   FROM jsonb_each(m.accounts) e WHERE jsonb_array_length(COALESCE(e.value->'readings','[]'::jsonb)-ids)>0),'{}'::jsonb)
 WHERE m.organization_id=org AND client_identity(m.organization_id,m.client_entity)=client_identity(org,name)
  AND EXISTS(SELECT 1 FROM jsonb_each(m.accounts) e WHERE COALESCE(e.value->'readings','[]'::jsonb) ?| ids);
 -- A company link keeps only the accounts a map still holds: a file's accounts leave the links with it.
 UPDATE books_company_links k SET accounts=COALESCE((SELECT jsonb_object_agg(e.key,e.value) FROM jsonb_each(k.accounts) e
   WHERE EXISTS(SELECT 1 FROM books_account_maps m WHERE m.organization_id=org AND client_identity(m.organization_id,m.client_entity)=client_identity(org,name) AND m.accounts ? e.key)),'{}'::jsonb)
 WHERE k.organization_id=org AND client_identity(k.organization_id,k.client_entity)=client_identity(org,name);

END $$;
COMMIT;
