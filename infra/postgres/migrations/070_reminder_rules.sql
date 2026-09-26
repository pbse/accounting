BEGIN;
-- A firm's reminder rule: each reminder still needs a person's approval; the rule sets how long after the previous
-- email was delivered one becomes due and how many a request can have. The link's seven-day expiry bounds the wait.
ALTER TABLE organizations ADD COLUMN reminder_after_days int NOT NULL DEFAULT 0 CHECK (reminder_after_days BETWEEN 0 AND 6),
 ADD COLUMN reminder_max int NOT NULL DEFAULT 3 CHECK (reminder_max BETWEEN 1 AND 5);
ALTER TABLE organization_policy_changes ADD COLUMN reminder_after_days int, ADD COLUMN reminder_max int;
COMMIT;
