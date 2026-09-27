BEGIN;
-- Whether the assistant may ask the model to write an answer to a close question none of its fixed reads matches.
ALTER TABLE organizations ADD COLUMN assistant_model_answers boolean NOT NULL DEFAULT false;
ALTER TABLE organization_policy_changes ADD COLUMN assistant_model_answers boolean;
COMMIT;
