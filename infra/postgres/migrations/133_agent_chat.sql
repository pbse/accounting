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
-- Firms whose written answers this migration stopped are told so in Firm settings until a level is chosen.
ALTER TABLE organizations ADD COLUMN agent_chat_stopped boolean NOT NULL DEFAULT false;
UPDATE organizations SET agent_chat_stopped=(assistant_model_answers AND NOT assistant_investigations) OR (assistant_firm_answers AND NOT (assistant_model_answers AND assistant_investigations));
-- The policy history records the level only on a change that set it; assistant_model_answers stays as the staff
-- planner's own consent (staff_planning in the policy route).
ALTER TABLE organization_policy_changes ADD COLUMN agent_chat text CHECK (agent_chat IN ('off','closes','firm'));
COMMIT;
