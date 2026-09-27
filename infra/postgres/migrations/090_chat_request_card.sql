BEGIN;
-- An assistant card can also save a consolidated client request draft; it never approves or sends one.
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_action_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_action_check CHECK (action IN ('prepare','recheck','stop','delegate','request'));
COMMIT;
