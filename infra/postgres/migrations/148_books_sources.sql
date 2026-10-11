BEGIN;
-- Source selection (docs/books-sources.md, Migration step 2; delivery step 5): which source a client's month's books
-- come from. Without a choice, each reader keeps today's rule, the newest capture; a person may name QuickBooks or Xero
-- for the client as a whole (its default) or for one month (an override), and every reader that picks between the two
-- honours it. Each choice is a new row with its reason; the newest for a month, else the newest default, is in force.
CREATE TABLE books_sources (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 -- period is the month an override is for; NULL is the client's default.
 period text CHECK (period IS NULL OR period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 source text NOT NULL CHECK (source IN ('newest','quickbooks','xero')),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX books_sources_client ON books_sources(organization_id,lower(client_entity),id DESC);
ALTER TABLE books_sources ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON books_sources FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON books_sources FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));

-- books_source is the source in force for a client's month, under any of its names: the month's override, else the
-- client's default, else 'newest'.
CREATE FUNCTION books_source(org text, name text, month text) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT COALESCE(
  (SELECT s.source FROM books_sources s WHERE s.organization_id=org AND client_identity(s.organization_id,s.client_entity)=client_identity(org,name) AND s.period=month ORDER BY s.id DESC LIMIT 1),
  (SELECT s.source FROM books_sources s WHERE s.organization_id=org AND client_identity(s.organization_id,s.client_entity)=client_identity(org,name) AND s.period IS NULL ORDER BY s.id DESC LIMIT 1),
  'newest')
$$;
COMMIT;
