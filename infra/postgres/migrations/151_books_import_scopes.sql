BEGIN;
-- Scope and basis (docs/books-sources.md, How an upload becomes the month's books, steps 3 and 5; delivery step 6): what
-- population a file's confirmed reading covers, and what basis the export represents, as a reviewer attests them. The
-- accounts and the month present come from the reading; the reviewer says which book it was exported from, any filters
-- the export applied, and whether it is the client's complete books for the month or what is missing. The basis is the
-- export's, a fact about the file, compared with the client's approved basis procedure, which stays the authority. Each
-- attestation is a version bound to the confirmed reading it attests: a layout confirmed again asks for it afresh.
CREATE TABLE books_import_scopes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 import_id bigint NOT NULL REFERENCES books_imports(id) ON DELETE CASCADE,
 layout_id bigint NOT NULL REFERENCES books_layouts(id) ON DELETE CASCADE,
 book text NOT NULL DEFAULT '' CHECK (length(book)<=200),
 filters text NOT NULL DEFAULT '' CHECK (length(filters)<=500),
 complete boolean NOT NULL,
 missing text NOT NULL DEFAULT '' CHECK (length(missing)<=500 AND (complete OR length(btrim(missing))>0)),
 basis text NOT NULL CHECK (basis IN ('accrual','cash','modified_cash')),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_import_scopes_import ON books_import_scopes(import_id,id DESC);
ALTER TABLE books_import_scopes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_import_scopes FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_import_scopes FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_remove ON books_import_scopes FOR DELETE USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
