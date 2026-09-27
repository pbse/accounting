BEGIN;
-- Whether a firm-wide question none of the assistant's fixed reads matches may go to the model, separately from close questions.
ALTER TABLE organizations ADD COLUMN assistant_firm_answers boolean NOT NULL DEFAULT false;
ALTER TABLE organization_policy_changes ADD COLUMN assistant_firm_answers boolean;
COMMIT;
