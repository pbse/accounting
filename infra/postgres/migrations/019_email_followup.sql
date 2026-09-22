BEGIN;
ALTER TABLE email_connections ADD COLUMN webhook_secret bytea;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_request_id_key;
ALTER TABLE email_outbox ADD COLUMN reminder_of text REFERENCES email_outbox(id);
CREATE UNIQUE INDEX email_initial_request ON email_outbox(request_id) WHERE reminder_of IS NULL;
CREATE UNIQUE INDEX email_one_followup ON email_outbox(reminder_of) WHERE reminder_of IS NOT NULL;
CREATE TABLE email_delivery_events (
 connection_id bigint NOT NULL REFERENCES email_connections(id) ON DELETE CASCADE,
 event_id text NOT NULL,
 body_digest text NOT NULL,
 provider_id text NOT NULL,
 recipient text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('sent','delivered','delivery_delayed','bounced','complained','failed','suppressed')),
 occurred_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(connection_id,event_id)
);
CREATE INDEX email_events_message ON email_delivery_events(connection_id,provider_id);
-- Keep early events even when the send response has not yet supplied its ID.
-- A later delivery event cannot erase a bounce, suppression or complaint.
CREATE VIEW email_delivery AS
 SELECT m.*, COALESCE((SELECT e.kind FROM email_delivery_events e JOIN work_requests q ON q.id=m.request_id
 WHERE e.connection_id=m.connection_id AND e.provider_id=m.provider_id AND lower(e.recipient)=lower(q.recipient)
 ORDER BY CASE WHEN e.kind IN ('bounced','complained','failed','suppressed') THEN 2 WHEN e.kind='delivered' THEN 1 ELSE 0 END DESC,e.occurred_at DESC,e.event_id DESC LIMIT 1),'unconfirmed') AS delivery
 FROM email_outbox m;
COMMIT;
