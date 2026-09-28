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
-- The audit log is append-only for everyone, whatever table grants a later migration run hands out: a
-- change to or removal of a recorded action is refused. A reviewed retention change would have to
-- disable this trigger in its own migration.
CREATE FUNCTION platform_audit_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'platform_audit is append-only';
END $$;
CREATE TRIGGER platform_audit_append_only BEFORE UPDATE OR DELETE ON platform_audit FOR EACH ROW EXECUTE FUNCTION platform_audit_append_only();
CREATE TRIGGER platform_audit_no_truncate BEFORE TRUNCATE ON platform_audit FOR EACH STATEMENT EXECUTE FUNCTION platform_audit_append_only();
