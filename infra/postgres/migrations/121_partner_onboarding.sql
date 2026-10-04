BEGIN;
-- The person on our team named as a firm's support owner.
ALTER TABLE organizations ADD COLUMN support_owner text NOT NULL DEFAULT '' CHECK (char_length(support_owner) <= 200);
-- Our team's setup and service time on a firm, before or beside any close, is recorded against the firm.
ALTER TABLE close_effort_entries ADD COLUMN organization_id text REFERENCES organizations(id),
 ALTER COLUMN close_id DROP NOT NULL,
 ADD CONSTRAINT close_effort_entries_firm CHECK (close_id IS NOT NULL OR (organization_id IS NOT NULL AND activity='assistance'));
DROP POLICY tenant ON close_effort_entries;
CREATE POLICY tenant ON close_effort_entries USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes) OR current_setting('app.organization',true)=organization_id);
COMMIT;
