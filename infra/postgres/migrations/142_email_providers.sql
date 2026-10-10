BEGIN;
-- Which integration sends a firm's client email. Krith's own sender is every firm's default: it sends from Krith's
-- verified domain with Krith's key and webhook, so its row holds no credential, domain or signing secret of the firm's.
-- A firm may connect its own instead (Resend today); each new integration adds its name to this check.
ALTER TABLE email_connections ADD COLUMN provider text NOT NULL DEFAULT 'resend' CHECK (provider IN ('krith','resend'));
ALTER TABLE email_connections ALTER COLUMN credential DROP NOT NULL;
ALTER TABLE email_connections ADD CONSTRAINT email_connections_provider_settings
 CHECK (CASE WHEN provider='krith' THEN credential IS NULL AND webhook_secret IS NULL AND domain_id='' ELSE credential IS NOT NULL END);
COMMIT;
