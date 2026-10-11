BEGIN;
-- File imports (docs/books-sources.md, Reading files; delivery step 6): a client's books uploaded as files, read with a
-- confirmed layout. They stay behind a firm setting, off for every firm, which staff switch on with a reason; step 9
-- turns it on for internal validation and the demonstration firm only.
ALTER TABLE organizations ADD COLUMN file_imports boolean NOT NULL DEFAULT false;

-- books_imports: one uploaded file for a client's month, kept whole with its digest, who sent it and when. The month is
-- the one the person says it holds; a replacement is a new import naming the one it replaces.
CREATE TABLE books_imports (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 period text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 255),
 digest text NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
 content bytea NOT NULL CHECK (octet_length(content) BETWEEN 1 AND 52428800),
 format text NOT NULL CHECK (format IN ('csv','xlsx')),
 replaces bigint REFERENCES books_imports(id),
 -- holds is what the sender confirmed the file holds: one client's books. A file holding several clients' books waits for
 -- their split into one view per client, kept under access covering them all.
 holds text NOT NULL DEFAULT 'one_client' CHECK (holds IN ('one_client')),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_imports_client ON books_imports(organization_id,lower(client_entity),period,id DESC);

-- books_layouts: a client's confirmed layouts, as versions (bookfiles.Layout as JSON), the latest per client current and
-- proposed again for its next file. An import read with a confirmed layout records it.
CREATE TABLE books_layouts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 import_id bigint NOT NULL REFERENCES books_imports(id),
 layout jsonb NOT NULL,
 -- reading is what the layout read from the import when it was confirmed: lines, totals, problems and notes.
 reading jsonb NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_layouts_client ON books_layouts(organization_id,lower(client_entity),id DESC);
CREATE INDEX books_layouts_import ON books_layouts(import_id,id DESC);

ALTER TABLE books_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_imports FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_imports FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE books_layouts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_layouts FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_layouts FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
