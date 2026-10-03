BEGIN;
-- A client's first close in the firm is its opening month: its balances are reviewed as the starting point before later
-- months can be signed off. Existing closes are not takeovers.
ALTER TABLE client_closes ADD COLUMN takeover boolean NOT NULL DEFAULT false;
-- The client a close's name belongs to: the one client the current confirmed aliases of that name point to, unless the
-- name was already a client's own (a close under it predates the alias); otherwise the name itself. Aliases are stored in
-- documents.NormalizeEntity's form.
CREATE FUNCTION client_identity(org text, name text) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT CASE WHEN count(DISTINCT lower(a.client_entity))=1 AND NOT bool_or(EXISTS(SELECT 1 FROM client_closes c WHERE c.organization_id=org AND lower(c.entity)=lower(name) AND c.created_at<a.confirmed_at)) THEN min(lower(a.client_entity)) END
  FROM client_aliases a WHERE a.organization_id=org AND a.retired_at IS NULL AND a.alias=trim(regexp_replace(lower(name),'[^[:alnum:]]+',' ','g'))),lower(name))
$$;
-- A reviewed opening baseline: the opening month's tie-out as the reviewer saw it, with a note for each account or
-- finding that becomes inherited cleanup. Rows are only ever added.
CREATE TABLE opening_baselines (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 digest text NOT NULL,
 content jsonb NOT NULL,
 notes jsonb NOT NULL,
 reviewed_by text NOT NULL,
 reviewed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX opening_baselines_close ON opening_baselines(close_id,id);
-- An inherited cleanup item of one reviewed baseline, resolved with a reference.
CREATE TABLE opening_cleanup_resolutions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 baseline_id bigint NOT NULL REFERENCES opening_baselines(id) ON DELETE CASCADE,
 item text NOT NULL CHECK (char_length(item) BETWEEN 1 AND 200),
 reference text NOT NULL CHECK (char_length(reference) BETWEEN 5 AND 2000),
 resolved_by text NOT NULL,
 resolved_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (baseline_id,item)
);
ALTER TABLE opening_baselines ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON opening_baselines FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON opening_baselines FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE opening_cleanup_resolutions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON opening_cleanup_resolutions FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON opening_cleanup_resolutions FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
