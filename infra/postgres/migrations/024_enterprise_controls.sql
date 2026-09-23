BEGIN;
CREATE TABLE organization_member_access (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL CHECK (left(subject,5)='user_' AND length(subject) BETWEEN 6 AND 200),
 granted boolean NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organization_member_access_current ON organization_member_access(organization_id,subject,id DESC);
CREATE VIEW current_organization_members AS SELECT DISTINCT ON (organization_id,subject) * FROM organization_member_access ORDER BY organization_id,subject,id DESC;

ALTER TABLE organizations ADD COLUMN max_closes integer NOT NULL DEFAULT 500 CHECK (max_closes BETWEEN 1 AND 10000);
ALTER TABLE organizations ADD COLUMN max_pending_jobs integer NOT NULL DEFAULT 250 CHECK (max_pending_jobs BETWEEN 1 AND 10000);
ALTER TABLE organizations ADD COLUMN disabled_at timestamptz;
ALTER TABLE organizations ADD COLUMN disabled_by text;
ALTER TABLE organizations ADD COLUMN disabled_reason text;

ALTER TABLE client_closes ADD COLUMN retain_until date;
ALTER TABLE client_closes ADD COLUMN legal_hold boolean NOT NULL DEFAULT false;
ALTER TABLE client_closes ADD COLUMN retention_version integer NOT NULL DEFAULT 0;
ALTER TABLE client_closes ADD COLUMN retention_reason text;
ALTER TABLE client_closes ADD COLUMN retention_actor text;
ALTER TABLE client_closes ADD COLUMN retention_changed_at timestamptz;

-- Link known delivery events to their close; preserve unmatched history for
-- operator review instead of discarding it during a migration.
ALTER TABLE email_delivery_events ADD COLUMN close_id bigint REFERENCES client_closes(id) ON DELETE CASCADE;
WITH unique_matches AS (
 SELECT e.connection_id,e.event_id,min(d.close_id) AS close_id
 FROM email_delivery_events e
 JOIN email_outbox m ON m.connection_id=e.connection_id AND m.provider_id=e.provider_id
 JOIN work_requests q ON q.id=m.request_id AND lower(e.recipient)=lower(q.recipient)
 JOIN request_drafts d ON d.id=q.draft_id
 GROUP BY e.connection_id,e.event_id
 HAVING count(DISTINCT d.close_id)=1
)
UPDATE email_delivery_events e SET close_id=u.close_id
FROM unique_matches u WHERE e.connection_id=u.connection_id AND e.event_id=u.event_id;
CREATE FUNCTION link_delivery_event_close() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.provider_id IS NOT NULL AND
  (SELECT count(DISTINCT d.close_id) FROM email_outbox m JOIN work_requests q ON q.id=m.request_id
   JOIN request_drafts d ON d.id=q.draft_id
   WHERE m.connection_id=NEW.connection_id AND m.provider_id=NEW.provider_id)=1 THEN
  UPDATE email_delivery_events e SET close_id=d.close_id
  FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id
  WHERE q.id=NEW.request_id AND e.connection_id=NEW.connection_id AND e.provider_id=NEW.provider_id
   AND lower(e.recipient)=lower(q.recipient) AND e.close_id IS NULL;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER link_delivery_event_close AFTER UPDATE OF provider_id ON email_outbox FOR EACH ROW EXECUTE FUNCTION link_delivery_event_close();

CREATE TABLE close_deletion_receipts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL,
 entity_digest text NOT NULL CHECK (length(entity_digest)=64),
 period text NOT NULL,
 production_postings integer NOT NULL CHECK (production_postings>=0),
 actor text NOT NULL,
 reason_code text NOT NULL CHECK (reason_code IN ('retention_expired','client_request','contract_termination')),
 deleted_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE recovery_actions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 job_id bigint NOT NULL REFERENCES preparation_jobs(id) ON DELETE CASCADE,
 action text NOT NULL CHECK (action IN ('retry_preparation')),
 prior_state text NOT NULL,
 actor text NOT NULL,
 reason text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION enforce_firm_pending_jobs() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE firm text; allowed integer; used integer;
BEGIN
 IF NEW.state NOT IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' THEN
  IF OLD.state IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client') THEN RETURN NEW; END IF;
 END IF;
 SELECT organization_id INTO firm FROM client_closes WHERE id=NEW.close_id;
 SELECT max_pending_jobs INTO allowed FROM organizations WHERE id=firm FOR UPDATE;
 SELECT count(*) INTO used FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id
 WHERE c.organization_id=firm AND j.state IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client') AND j.id IS DISTINCT FROM NEW.id;
 IF used>=allowed THEN RAISE EXCEPTION 'Firm active job limit reached' USING ERRCODE='P0001'; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER firm_pending_jobs BEFORE INSERT OR UPDATE OF state ON preparation_jobs FOR EACH ROW EXECUTE FUNCTION enforce_firm_pending_jobs();
COMMIT;
