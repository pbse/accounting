BEGIN;
-- One firm setting for the agent chat: off, on for client closes, or on firm-wide. The three settings it replaces
-- (written answers, firm-wide answers, investigations) stop being read; they are dropped once nothing reads them.
-- The loop lets the model choose its reads, which is what investigations consented to, so a firm that turned on
-- written answers without investigations lands at off and needs a new administrator decision; a firm that consented
-- to close questions alone is never moved to firm-wide questions.
ALTER TABLE organizations ADD COLUMN agent_chat text NOT NULL DEFAULT 'off' CHECK (agent_chat IN ('off','closes','firm'));
UPDATE organizations SET agent_chat=CASE
 WHEN assistant_model_answers AND assistant_investigations AND assistant_firm_answers THEN 'firm'
 WHEN assistant_model_answers AND assistant_investigations THEN 'closes'
 ELSE 'off' END;
ALTER TABLE organization_policy_changes ADD COLUMN agent_chat text CHECK (agent_chat IN ('off','closes','firm'));
COMMIT;
