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
 -- Why the file replaces the one it names: a replacement always says.
 replaces_reason text NOT NULL DEFAULT '' CHECK (length(replaces_reason)<=500 AND (replaces IS NULL OR length(replaces_reason)>=1)),
 -- holds is what the sender confirmed the file holds: one client's books. A file holding several clients' books waits for
 -- their split into one view per client, kept under access covering them all.
 holds text NOT NULL DEFAULT 'one_client' CHECK (holds IN ('one_client')),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_imports_client ON books_imports(organization_id,lower(client_entity),period,id DESC);
-- The same file sent again for the same month is the one kept: a retry, however it overlaps the first, adds nothing.
CREATE UNIQUE INDEX books_imports_once ON books_imports(organization_id,lower(client_entity),period,digest);

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
 -- A CSV's encoding and delimiter as the person confirmed them, when the reader's own guess was not the one kept.
 encoding text NOT NULL DEFAULT '' CHECK (encoding IN ('','utf-8','utf-16le','utf-16be','windows-1252')),
 delimiter text NOT NULL DEFAULT '' CHECK (delimiter IN ('','comma','semicolon','tab','pipe')),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_layouts_client ON books_layouts(organization_id,lower(client_entity),id DESC);
CREATE INDEX books_layouts_import ON books_layouts(import_id,id DESC);

-- books_import_pages: an import's rows as read, two hundred to a chunk with the number of its first row, kept the first
-- time the file is read a given way (parse names the encoding and delimiter that decided how), so a later page is read
-- from here rather than by reading the whole file again. The file never changes, so neither do its rows read that way;
-- only the latest way it was read is kept.
CREATE TABLE books_import_pages (
 organization_id text NOT NULL REFERENCES organizations(id),
 import_id bigint NOT NULL REFERENCES books_imports(id) ON DELETE CASCADE,
 parse text NOT NULL,
 position int NOT NULL,
 table_name text NOT NULL,
 hidden boolean NOT NULL,
 hidden_columns jsonb NOT NULL,
 total int NOT NULL,
 chunk int NOT NULL,
 first_row int NOT NULL,
 rows jsonb NOT NULL,
 -- meta is what the file was read as: its encoding, delimiter and what was unsure, its linked workbooks, whether it
 -- recalculates on open and how many formulas it holds.
 meta jsonb NOT NULL DEFAULT '{}'::jsonb,
 PRIMARY KEY (import_id,parse,position,chunk)
);
ALTER TABLE books_import_pages ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_import_pages FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_import_pages FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_remove ON books_import_pages FOR DELETE USING (current_setting('app.organization',true) IN ('*',organization_id));

-- books_import_previews: a file's latest preview reading, under the digest of what decided it (how the file was read
-- and the layout), so its further lines are paged from here rather than by reading the file again.
CREATE TABLE books_import_previews (
 import_id bigint PRIMARY KEY REFERENCES books_imports(id) ON DELETE CASCADE,
 organization_id text NOT NULL REFERENCES organizations(id),
 digest text NOT NULL,
 reading jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE books_import_previews ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_import_previews FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_import_previews FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_remove ON books_import_previews FOR DELETE USING (current_setting('app.organization',true) IN ('*',organization_id));

-- books_import_deletions: a receipt for each file deleted (sent in error, or at the client's request), without its
-- content: what it was, its digest, why it was deleted and by whom.
CREATE TABLE books_import_deletions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 period text NOT NULL,
 name text NOT NULL,
 digest text NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 deleted_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE books_import_deletions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_import_deletions FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_import_deletions FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

ALTER TABLE books_imports ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_imports FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_imports FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE books_layouts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_layouts FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_layouts FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
-- A file and its readings are removed only with the close of their month, when it is deleted under its retention.
CREATE POLICY tenant_remove ON books_imports FOR DELETE USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_remove ON books_layouts FOR DELETE USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
