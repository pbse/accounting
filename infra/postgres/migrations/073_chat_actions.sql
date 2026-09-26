BEGIN;
-- An assistant answer can carry one close action card; outcome records what its confirmation started.
ALTER TABLE chat_messages ADD COLUMN action text CHECK (action IN ('prepare','recheck','stop'));
ALTER TABLE chat_messages ADD COLUMN outcome jsonb CHECK (jsonb_typeof(outcome)='object');
ALTER TABLE chat_messages ADD CONSTRAINT chat_action_in_close CHECK ((action IS NULL OR (close_id IS NOT NULL AND role='assistant')) AND (outcome IS NULL OR action IS NOT NULL));
COMMIT;
