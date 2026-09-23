BEGIN;
CREATE TABLE quickbooks_entity_syncs (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 connection_id bigint NOT NULL REFERENCES quickbooks_connections(id),
 period text NOT NULL CHECK(period ~ '^[0-9]{4}-[0-9]{2}$'),
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','completed','failed')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 claim text,
 claimed_at timestamptz,
 started_at timestamptz,
 finished_at timestamptz,
 payload jsonb,
 digest text,
 evidence_id bigint,
 error text NOT NULL DEFAULT '',
 requested_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT quickbooks_entity_source_scope FOREIGN KEY(close_id,evidence_id) REFERENCES evidence(close_id,id)
);
CREATE UNIQUE INDEX quickbooks_entity_sync_active ON quickbooks_entity_syncs(close_id,connection_id,period) WHERE state IN ('queued','running');
CREATE INDEX quickbooks_entity_sync_pending ON quickbooks_entity_syncs(next_attempt_at,id) WHERE state IN ('queued','running');
COMMIT;
