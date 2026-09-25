BEGIN;
-- A keyed POST runs at most once per firm within 24 hours. Only its fingerprint and final status are kept, never the response.
CREATE TABLE idempotency_keys (
 organization_id text NOT NULL REFERENCES organizations(id),
 key text NOT NULL CHECK (length(key) BETWEEN 1 AND 255),
 fingerprint text NOT NULL,
 status integer,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (organization_id, key)
);
ALTER TABLE idempotency_keys ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON idempotency_keys USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
