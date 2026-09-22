BEGIN;
CREATE TABLE client_request_links (
 digest text PRIMARY KEY,
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 issued_by text NOT NULL,
 issued_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL DEFAULT now()+interval '7 days',
 revoked_at timestamptz,
 used_at timestamptz,
 reply_digest text
);
CREATE INDEX client_request_links_request ON client_request_links(request_id,issued_at DESC);
COMMIT;
