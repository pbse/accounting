BEGIN;
CREATE TABLE forwarding_links (
 digest text PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 issued_by text NOT NULL,
 issued_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '7 days',
 revoked_at timestamptz
);
CREATE INDEX forwarding_links_close ON forwarding_links(close_id,issued_at DESC);
CREATE TABLE forwarded_files (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 link_digest text NOT NULL REFERENCES forwarding_links(digest),
 message_digest text NOT NULL,
 from_address text NOT NULL,
 subject text NOT NULL,
 name text NOT NULL,
 body bytea NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 5242880),
 digest text NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','scanning','clean','failed','rejected','released')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
 claim text,
 claimed_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 scanned_digest text,
 scanned_at timestamptz,
 scan_version text,
 source_id bigint,
 error text NOT NULL DEFAULT '',
 decided_by text,
 decision_note text,
 received_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(close_id,message_digest,digest),
 CONSTRAINT forwarded_source_scope FOREIGN KEY(close_id,source_id) REFERENCES evidence(close_id,id)
);
CREATE INDEX forwarded_files_pending ON forwarded_files(next_attempt_at,id) WHERE state IN ('queued','scanning');
COMMIT;
