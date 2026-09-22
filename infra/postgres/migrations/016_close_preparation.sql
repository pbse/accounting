BEGIN;
CREATE TABLE close_preparations (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 inventory_digest text NOT NULL,
 scope jsonb NOT NULL CHECK(jsonb_typeof(scope)='array'),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(close_id,inventory_digest)
);
COMMIT;
