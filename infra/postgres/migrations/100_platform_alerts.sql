BEGIN;
-- Platform alerts (docs/admin-panel.md, A4): problems with the platform itself, not one firm's work, delivered to the
-- operations webhook like a firm's alerts. One open alert per kind and item; it resolves when the problem clears.
CREATE TABLE platform_alerts (
 id bigserial PRIMARY KEY,
 kind text NOT NULL,
 item_id text NOT NULL,
 detail text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sending','accepted','unknown')),
 created_at timestamptz NOT NULL DEFAULT now(),
 claimed_at timestamptz,
 finished_at timestamptz,
 resolved_at timestamptz,
 error text NOT NULL DEFAULT '',
 CHECK (resolved_at IS NULL OR resolved_at>=created_at)
);
CREATE UNIQUE INDEX platform_alert_open ON platform_alerts(kind,item_id) WHERE resolved_at IS NULL;
ALTER TABLE platform_alerts ENABLE ROW LEVEL SECURITY;
CREATE POLICY platform ON platform_alerts USING (current_setting('app.organization',true)='*');
COMMIT;
