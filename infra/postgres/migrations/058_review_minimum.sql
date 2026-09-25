BEGIN;
-- A firm can require an independent reviewer only for drafts whose proposed entries reach an amount.
ALTER TABLE organizations ADD COLUMN independent_review_min_amount numeric(14,2) CHECK (independent_review_min_amount > 0);
ALTER TABLE organizations ADD CONSTRAINT organizations_review_minimum_hosted CHECK (id <> 'local-development' OR independent_review_min_amount IS NULL);
ALTER TABLE organization_policy_changes ADD COLUMN independent_review_min_amount numeric(14,2);
COMMIT;
