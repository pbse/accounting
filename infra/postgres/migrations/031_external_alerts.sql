BEGIN;
CREATE TABLE operations_alert_deliveries (
 id bigserial PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 kind text NOT NULL,
 item_id text NOT NULL,
 item_state text NOT NULL,
 age_minutes integer NOT NULL,
 status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sending','accepted','unknown')),
 created_at timestamptz NOT NULL DEFAULT now(),
 claimed_at timestamptz,
 finished_at timestamptz,
 resolved_at timestamptz,
 error text NOT NULL DEFAULT '',
 CHECK (resolved_at IS NULL OR resolved_at>=created_at)
);
CREATE UNIQUE INDEX operations_alert_open ON operations_alert_deliveries(organization_id,kind,item_id,item_state) WHERE resolved_at IS NULL;
COMMIT;
