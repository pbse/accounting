-- Admin panel for platform staff (docs/admin-panel.md): an append-only log of every staff action
-- and each background loop's last sign of life.

CREATE TABLE platform_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 staff_user text NOT NULL CHECK (staff_user <> ''),
 action text NOT NULL CHECK (action <> ''),
 organization_id text REFERENCES organizations(id), -- the firm affected, if any
 target_type text NOT NULL DEFAULT '',
 target_id text NOT NULL DEFAULT '',
 reason text NOT NULL CHECK (reason <> ''),
 request jsonb NOT NULL DEFAULT '{}'::jsonb,
 result text NOT NULL CHECK (result IN ('ok','refused')),
 error text NOT NULL DEFAULT '',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_audit_firm ON platform_audit (organization_id, created_at DESC);
CREATE INDEX platform_audit_staff ON platform_audit (staff_user, created_at DESC);

CREATE TABLE worker_heartbeats (
 loop text PRIMARY KEY CHECK (loop <> ''),
 instance text NOT NULL DEFAULT '',
 beat_at timestamptz NOT NULL DEFAULT now(),
 detail jsonb NOT NULL DEFAULT '{}'::jsonb
);

-- Staff actions are visible to system work and, later, to the firm they affected.
ALTER TABLE platform_audit ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON platform_audit USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE worker_heartbeats ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON worker_heartbeats USING (current_setting('app.organization',true)='*');
-- The audit log is append-only for the runtime role (grants follow the owner's default privileges).
DO $$ DECLARE r record; BEGIN
 FOR r IN SELECT grantee FROM information_schema.role_table_grants WHERE table_schema='public' AND table_name='platform_audit' AND privilege_type IN ('UPDATE','DELETE') AND grantee<>current_user LOOP
  EXECUTE format('REVOKE UPDATE, DELETE ON platform_audit FROM %I', r.grantee);
 END LOOP;
END $$;
