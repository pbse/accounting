BEGIN;
-- A firm's materiality: a Trial Balance difference below it, with no proposed entry pending, is shown as within materiality.
ALTER TABLE organizations ADD COLUMN materiality_amount numeric(14,2) CHECK (materiality_amount > 0);
ALTER TABLE organization_policy_changes ADD COLUMN materiality_amount numeric(14,2);
COMMIT;
