BEGIN;
-- A published management pack is frozen: rows are only ever added, a correction is a new version naming the one it supersedes.
CREATE TABLE management_publications (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 version int NOT NULL CHECK (version >= 1),
 pack jsonb NOT NULL,
 digest text NOT NULL,
 commentary text NOT NULL CHECK (char_length(commentary) BETWEEN 1 AND 4000),
 supersedes bigint REFERENCES management_publications(id),
 reason text NOT NULL DEFAULT '' CHECK (char_length(reason) <= 2000 AND (supersedes IS NULL OR reason <> '')),
 published_by text NOT NULL,
 published_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (close_id, version)
);
CREATE TABLE management_acknowledgments (
 publication_id bigint NOT NULL REFERENCES management_publications(id) ON DELETE CASCADE,
 subject text NOT NULL,
 acknowledged_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (publication_id, subject)
);
ALTER TABLE management_publications ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON management_publications FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON management_publications FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE management_acknowledgments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON management_acknowledgments FOR SELECT USING (publication_id IN (SELECT id FROM management_publications));
CREATE POLICY tenant_add ON management_acknowledgments FOR INSERT WITH CHECK (publication_id IN (SELECT id FROM management_publications));
COMMIT;
