BEGIN;
-- The agent chat level (migration 133) replaced three settings that nothing has read since; they are dropped from
-- the firm and from its policy history. The staff planner's own consent, assistant_model_answers, stays
-- (staff_planning in the policy route).
ALTER TABLE organizations DROP COLUMN assistant_firm_answers, DROP COLUMN assistant_investigations;
ALTER TABLE organization_policy_changes DROP COLUMN assistant_firm_answers, DROP COLUMN assistant_investigations;
-- The agent may propose opening a client's month from the dashboard conversation (open_close). The card names a
-- client and a month, not a close: confirmation opens the close and sets it as the card's close in one transaction.
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_action_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_action_check CHECK (action IN ('prepare','recheck','stop','delegate','request','approval','portfolio','open_close'));
ALTER TABLE chat_messages DROP CONSTRAINT chat_action_scope;
ALTER TABLE chat_messages ADD CONSTRAINT chat_action_scope CHECK (
 (action IS NULL AND proposal IS NULL AND target_close_id IS NULL AND outcome IS NULL)
 OR (action IS NOT NULL AND role='assistant' AND (close_id IS NOT NULL OR target_close_id IS NOT NULL OR action IN ('portfolio','open_close'))));
-- An answer names its question, so a card's confirmation reaches the question it was asked by and no other, however
-- the author's requests interleaved; the question's deletion takes its answer.
ALTER TABLE chat_messages ADD COLUMN question_id bigint REFERENCES chat_messages(id) ON DELETE CASCADE;
CREATE INDEX chat_messages_question ON chat_messages (question_id) WHERE question_id IS NOT NULL;
COMMIT;
