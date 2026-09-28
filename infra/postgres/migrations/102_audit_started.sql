-- A staff action with an effect outside the database (WorkOS) records that it started, and commits that, before the
-- effect, so the audit shows the attempt even when its outcome cannot be recorded; the outcome follows as its own entry.
ALTER TABLE platform_audit DROP CONSTRAINT platform_audit_result_check;
ALTER TABLE platform_audit ADD CONSTRAINT platform_audit_result_check CHECK (result IN ('ok','refused','started'));
