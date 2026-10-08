BEGIN;
-- The agent loop (docs/agent-chat.md): a card can now be any action the model proposes, a firm-wide card names the
-- close it acts on, and every turn pair records the closes its reads touched so a lost client's facts can be removed.
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_action_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_action_check CHECK (action IN ('prepare','recheck','stop','delegate','request','approval','portfolio'));
-- proposal: the model's arguments as the card shows them. steps: the trace shown with an answer (id, tool, title,
-- status, size, link; never content). closes: the closes the pair's reads touched, on both messages of the pair.
ALTER TABLE chat_messages ADD COLUMN proposal jsonb CHECK (jsonb_typeof(proposal)='object');
ALTER TABLE chat_messages ADD COLUMN target_close_id bigint REFERENCES client_closes(id);
ALTER TABLE chat_messages ADD COLUMN steps jsonb CHECK (jsonb_typeof(steps)='array');
ALTER TABLE chat_messages ADD COLUMN closes bigint[] NOT NULL DEFAULT '{}';
ALTER TABLE chat_messages DROP CONSTRAINT chat_action_in_close;
-- An action is the agent's, on the conversation's close or a firm-wide card's target close; a portfolio card carries
-- its goal and closes in the proposal instead. An outcome needs an action.
ALTER TABLE chat_messages ADD CONSTRAINT chat_action_scope CHECK (
 (action IS NULL AND proposal IS NULL AND target_close_id IS NULL AND outcome IS NULL)
 OR (action IS NOT NULL AND role='assistant' AND (close_id IS NOT NULL OR target_close_id IS NOT NULL OR action='portfolio')));
CREATE INDEX chat_messages_closes ON chat_messages USING gin (closes);
-- Each attempt records its scope, rounds and tool calls; firm-wide attempts are recorded too.
ALTER TABLE assistant_answers ADD COLUMN scope text NOT NULL DEFAULT 'close' CHECK (scope IN ('firm','close'));
ALTER TABLE assistant_answers ADD COLUMN rounds smallint;
ALTER TABLE assistant_answers ADD COLUMN tool_calls smallint;
COMMIT;
