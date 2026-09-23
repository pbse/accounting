BEGIN;
CREATE TABLE inbound_replies (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 outbox_id text NOT NULL REFERENCES email_outbox(id) ON DELETE CASCADE,
 message_digest text NOT NULL,
 envelope_from text NOT NULL,
 header_from text NOT NULL,
 body text NOT NULL,
 addresses_match boolean NOT NULL,
 state text NOT NULL DEFAULT 'held' CHECK(state IN ('held','released','rejected')),
 received_at timestamptz NOT NULL DEFAULT now(),
 decided_by text,
 decision_note text,
 verification_reference text,
 decided_at timestamptz,
 UNIQUE(outbox_id,message_digest)
);
CREATE INDEX inbound_replies_close ON inbound_replies(close_id,received_at DESC);
COMMIT;
