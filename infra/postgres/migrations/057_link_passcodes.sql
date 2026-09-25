BEGIN;
-- A firm can require that each new client request link also needs a passcode a person gives the client another way.
ALTER TABLE organizations ADD COLUMN require_link_passcode boolean NOT NULL DEFAULT false;
ALTER TABLE organization_policy_changes ADD COLUMN link_passcode boolean NOT NULL DEFAULT false;
ALTER TABLE client_request_links ADD COLUMN passcode_digest text, ADD COLUMN passcode_failures integer NOT NULL DEFAULT 0;
COMMIT;
