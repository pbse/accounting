BEGIN;
-- Row-level security binds the runtime role, which must not own these tables or bypass RLS.
-- app.organization is set per statement by the API: a firm ID, '*' for system work, unset denies.
DO $$
DECLARE t record;
BEGIN
  FOR t IN SELECT c.relname,
      EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attname='organization_id' AND NOT a.attisdropped) AS org,
      EXISTS(SELECT 1 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attname='close_id' AND NOT a.attisdropped) AS close
    FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind='r'
  LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t.relname);
    IF t.org THEN
      EXECUTE format($p$CREATE POLICY tenant ON %I USING (current_setting('app.organization',true) IN ('*',organization_id))$p$, t.relname);
    ELSIF t.close THEN
      EXECUTE format($p$CREATE POLICY tenant ON %I USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes))$p$, t.relname);
    END IF;
  END LOOP;
  FOR t IN SELECT viewname FROM pg_views WHERE schemaname='public' LOOP
    EXECUTE format('ALTER VIEW %I SET (security_invoker=true)', t.viewname);
  END LOOP;
END $$;
CREATE POLICY tenant ON organizations USING (current_setting('app.organization',true) IN ('*',id));
CREATE POLICY tenant ON workpaper_reviews USING (current_setting('app.organization',true)='*' OR workpaper_id IN (SELECT id FROM workpapers));
CREATE POLICY tenant ON work_requests USING (current_setting('app.organization',true)='*' OR draft_id IN (SELECT id FROM request_drafts));
CREATE POLICY tenant ON work_replies USING (current_setting('app.organization',true)='*' OR request_id IN (SELECT id FROM work_requests));
CREATE POLICY tenant ON work_request_targets USING (current_setting('app.organization',true)='*' OR request_id IN (SELECT id FROM work_requests));
CREATE POLICY tenant ON client_request_links USING (current_setting('app.organization',true)='*' OR request_id IN (SELECT id FROM work_requests));
CREATE POLICY tenant ON email_outbox USING (current_setting('app.organization',true)='*' OR connection_id IN (SELECT id FROM email_connections));
CREATE POLICY tenant ON preparation_intakes USING (current_setting('app.organization',true)='*' OR job_id IN (SELECT id FROM preparation_jobs));
CREATE POLICY tenant ON preparation_answers USING (current_setting('app.organization',true)='*' OR job_id IN (SELECT job_id FROM preparation_intakes));
CREATE POLICY tenant ON quickbooks_transaction_rows USING (current_setting('app.organization',true)='*' OR sync_id IN (SELECT id FROM quickbooks_entity_syncs));
CREATE POLICY tenant ON quickbooks_ledger_selections USING (current_setting('app.organization',true)='*' OR snapshot_id IN (SELECT id FROM quickbooks_snapshots));
CREATE POLICY tenant ON procedure_example_approvals USING (current_setting('app.organization',true)='*' OR draft_id IN (SELECT id FROM procedure_drafts));
COMMIT;
