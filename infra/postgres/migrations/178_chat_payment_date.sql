BEGIN;
-- The agent proposes a date; only a human-confirmed external handoff uses the existing open-item decision route.
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_action_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_action_check CHECK (action IN ('prepare','recheck','stop','delegate','request','approval','portfolio','open_close','bill_pay'));
COMMIT;
