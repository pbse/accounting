BEGIN;
-- A client's confirmed profile (docs/books-sources.md, Client profile): the currency and fiscal-year start its books are
-- kept in, independent of any connection, effective from a month, as a history. For a month, the profile is the row of
-- the client (by client identity) with the latest effective month up to it, the latest recorded first. It refers to the
-- approved basis procedure rather than holding a basis. Where a connection reports otherwise, the difference is a
-- question, not an override.
CREATE TABLE client_profiles (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 effective_from text NOT NULL CHECK (effective_from ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 fiscal_year_start smallint NOT NULL CHECK (fiscal_year_start BETWEEN 1 AND 12),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX client_profiles_client ON client_profiles(organization_id,lower(client_entity),effective_from DESC,id DESC);
ALTER TABLE client_profiles ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON client_profiles FOR SELECT USING (current_setting('app.organization',true) IN ('*',organization_id));
CREATE POLICY tenant_add ON client_profiles FOR INSERT WITH CHECK (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
