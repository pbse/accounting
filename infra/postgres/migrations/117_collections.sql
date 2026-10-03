BEGIN;
-- The client's authority for the firm to contact its customers about money owed, from the named sender. The newest
-- record per client is in force; rows are only ever added.
CREATE TABLE customer_contact_authority (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (char_length(client_entity) BETWEEN 1 AND 200),
 granted boolean NOT NULL,
 sender text NOT NULL DEFAULT '' CHECK (char_length(sender) <= 320 AND (sender <> '' OR NOT granted)),
 reference text NOT NULL CHECK (char_length(reference) BETWEEN 5 AND 2000),
 recorded_by text NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT now()
);
-- A customer contact the client says not to email (or may email again). The newest record per address is in force.
CREATE TABLE collection_contact_preferences (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (char_length(client_entity) BETWEEN 1 AND 200),
 recipient text NOT NULL CHECK (char_length(recipient) BETWEEN 3 AND 320),
 do_not_contact boolean NOT NULL,
 reason text NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 2000),
 recorded_by text NOT NULL,
 recorded_at timestamptz NOT NULL DEFAULT now()
);
-- A drafted collection message about one open invoice, bound to the capture it was drafted from.
CREATE TABLE collection_messages (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 client_entity text NOT NULL,
 company text NOT NULL,
 capture_id text NOT NULL,
 item_id text NOT NULL,
 label text NOT NULL,
 customer text NOT NULL,
 amount numeric(18,2) NOT NULL CHECK (amount > 0),
 currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
 recipient text NOT NULL CHECK (char_length(recipient) BETWEEN 3 AND 320),
 reply_to text NOT NULL DEFAULT '' CHECK (char_length(reply_to) <= 320),
 subject text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 200),
 body text NOT NULL CHECK (char_length(body) BETWEEN 10 AND 5000),
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX collection_messages_close ON collection_messages(close_id,id);
-- An outbox message is either a client request's email (with its private link) or one collection message.
DROP VIEW email_delivery;
ALTER TABLE email_outbox ALTER COLUMN request_id DROP NOT NULL, ALTER COLUMN link_digest DROP NOT NULL,
 ADD COLUMN collection_id bigint REFERENCES collection_messages(id) ON DELETE CASCADE,
 ADD CONSTRAINT email_outbox_subject CHECK ((request_id IS NULL) <> (collection_id IS NULL) AND (request_id IS NULL) = (link_digest IS NULL));
CREATE UNIQUE INDEX email_outbox_collection ON email_outbox(collection_id) WHERE collection_id IS NOT NULL;
CREATE VIEW email_delivery AS
 SELECT m.*, COALESCE((SELECT e.kind FROM email_delivery_events e
  WHERE e.connection_id=m.connection_id AND e.provider_id=m.provider_id
  AND lower(e.recipient)=lower(COALESCE((SELECT q.recipient FROM work_requests q WHERE q.id=m.request_id),(SELECT c.recipient FROM collection_messages c WHERE c.id=m.collection_id)))
  ORDER BY CASE WHEN e.kind IN ('bounced','complained','failed','suppressed') THEN 2 WHEN e.kind='delivered' THEN 1 ELSE 0 END DESC,e.occurred_at DESC,e.event_id DESC LIMIT 1),'unconfirmed') AS delivery
 FROM email_outbox m;
ALTER VIEW email_delivery SET (security_invoker=true);
-- Addresses whose collection email ever bounced, was marked as spam or was suppressed, whatever happened after.
CREATE VIEW collection_bounces AS
 SELECT DISTINCT c.organization_id, lower(c.client_entity) AS client_entity, lower(c.recipient) AS recipient
 FROM collection_messages c JOIN email_outbox m ON m.collection_id=c.id
 JOIN email_delivery_events e ON e.connection_id=m.connection_id AND e.provider_id=m.provider_id AND lower(e.recipient)=lower(c.recipient)
 WHERE e.kind IN ('bounced','complained','suppressed');
ALTER VIEW collection_bounces SET (security_invoker=true);
-- An event that arrives before the provider id is stored is linked to its close then, for collection messages too.
CREATE OR REPLACE FUNCTION link_delivery_event_close() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.provider_id IS NOT NULL AND
  (SELECT count(DISTINCT COALESCE(d.close_id,cm.close_id)) FROM email_outbox m LEFT JOIN work_requests q ON q.id=m.request_id
   LEFT JOIN request_drafts d ON d.id=q.draft_id LEFT JOIN collection_messages cm ON cm.id=m.collection_id
   WHERE m.connection_id=NEW.connection_id AND m.provider_id=NEW.provider_id)=1 THEN
  UPDATE email_delivery_events e SET close_id=COALESCE(d.close_id,cm.close_id)
  FROM email_outbox m LEFT JOIN work_requests q ON q.id=m.request_id LEFT JOIN request_drafts d ON d.id=q.draft_id
   LEFT JOIN collection_messages cm ON cm.id=m.collection_id
  WHERE m.id=NEW.id AND e.connection_id=NEW.connection_id AND e.provider_id=NEW.provider_id
   AND lower(e.recipient)=lower(COALESCE(q.recipient,cm.recipient)) AND e.close_id IS NULL;
 END IF;
 RETURN NEW;
END $$;
ALTER TABLE customer_contact_authority ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON customer_contact_authority USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE collection_contact_preferences ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON collection_contact_preferences USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE collection_messages ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON collection_messages USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
