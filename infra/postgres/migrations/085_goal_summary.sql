BEGIN;
-- What the agent last found for a delegated close, for Work's list of them: needs (what people owe, counted as the goal
-- card counts it), ready (every current document has a draft ready for review), signoff (the sign-off state once read)
-- and as_of (when it was found). Empty until the agent's first step.
ALTER TABLE close_goals ADD COLUMN summary jsonb NOT NULL DEFAULT '{}';
COMMIT;
