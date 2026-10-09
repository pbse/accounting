BEGIN;
-- Krith sends a firm's invitations itself (docs/sign-in.md#invitations), from its own sender like the welcome emails,
-- instead of WorkOS's email. An invitation email names its invitation, who sent it, and the link's WorkOS invitation
-- token, sealed while the email waits and cleared once it is done. Each send or resend is its own row.
ALTER TABLE platform_emails DROP CONSTRAINT platform_emails_kind_check;
ALTER TABLE platform_emails ADD CONSTRAINT platform_emails_kind_check CHECK (kind IN ('welcome','joined','invitation'));
ALTER TABLE platform_emails ALTER COLUMN subject DROP NOT NULL;
ALTER TABLE platform_emails ADD COLUMN invitation bigint REFERENCES member_invitations(id) ON DELETE CASCADE;
ALTER TABLE platform_emails ADD COLUMN inviter text NOT NULL DEFAULT '' CHECK (length(inviter) <= 200);
ALTER TABLE platform_emails ADD COLUMN link bytea;
ALTER TABLE platform_emails ADD CONSTRAINT platform_emails_shape CHECK (CASE WHEN kind = 'invitation' THEN invitation IS NOT NULL AND subject IS NULL
 ELSE subject IS NOT NULL AND invitation IS NULL AND link IS NULL END);
CREATE INDEX platform_emails_invitation ON platform_emails(invitation) WHERE invitation IS NOT NULL;
COMMIT;
