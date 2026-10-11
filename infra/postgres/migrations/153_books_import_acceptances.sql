BEGIN;
-- Accepting a file's reading (docs/books-sources.md, How an upload becomes the month's books, steps 6 and 7; delivery
-- step 6): a member with review authority accepts a confirmed reading as the client's books for its month, from the
-- named system, exported on the named date, with the attested scope and basis and whether the final adjustments are in.
-- The checks run on the reading; each passes, holds what depends on it, or is a note, and none refuses the acceptance.
-- What they found is kept with it, beside the reading, the attestation and the account map it was given on. A layout
-- confirmed again, or a scope attested again, leaves the acceptance standing on record but no longer current.
CREATE TABLE books_import_acceptances (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 import_id bigint NOT NULL REFERENCES books_imports(id) ON DELETE CASCADE,
 layout_id bigint NOT NULL REFERENCES books_layouts(id) ON DELETE CASCADE,
 scope_id bigint NOT NULL REFERENCES books_import_scopes(id) ON DELETE CASCADE,
 -- map_stamp is the client's account map as it stood: its version and content.
 map_stamp text NOT NULL DEFAULT '',
 period text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 system text NOT NULL CHECK (length(system) BETWEEN 1 AND 200),
 exported_on date NOT NULL,
 final_adjustments boolean NOT NULL,
 -- explained is the reviewer's account of the prior accepted month's accounts this reading does not name.
 explained text NOT NULL DEFAULT '' CHECK (length(explained)<=1000),
 checks jsonb NOT NULL CHECK (jsonb_typeof(checks)='array'),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_import_acceptances_import ON books_import_acceptances(import_id,id DESC);
CREATE INDEX books_import_acceptances_client ON books_import_acceptances(organization_id,lower(client_entity),period,id DESC);
ALTER TABLE books_import_acceptances ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_import_acceptances FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_import_acceptances FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_remove ON books_import_acceptances FOR DELETE USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
