BEGIN;
CREATE TABLE IF NOT EXISTS client_closes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 entity text NOT NULL CHECK (length(entity) BETWEEN 1 AND 200),
 period text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS client_closes_entity_period ON client_closes (lower(entity), period);
CREATE TABLE IF NOT EXISTS workpapers (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id),
 kind text NOT NULL CHECK (kind IN ('prepaid','reconciliation')),
 digest text NOT NULL,
 paper jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(close_id, kind, digest)
);
COMMIT;
