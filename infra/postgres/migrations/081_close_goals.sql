BEGIN;
-- A close handed to the agent. It keeps the preparation scope current, is reached while every current document has a
-- draft ready for review, and keeps watching until the close is signed off or a person stops it. scope_digest is the
-- document scope it last brought into preparation; events is its history, oldest first.
CREATE TABLE close_goals (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 author text NOT NULL,
 due_on date,
 state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','reached','stopped')),
 scope_digest text NOT NULL DEFAULT '',
 events jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(events)='array'),
 created_at timestamptz NOT NULL DEFAULT now(),
 reached_at timestamptz,
 checked_at timestamptz,
 finished_at timestamptz,
 CHECK ((state='reached')=(reached_at IS NOT NULL)),
 CHECK ((state='stopped')=(finished_at IS NOT NULL))
);
CREATE UNIQUE INDEX close_goal_open ON close_goals(close_id) WHERE state<>'stopped';
CREATE INDEX close_goal_close ON close_goals(close_id,id DESC);
CREATE INDEX close_goal_next ON close_goals(checked_at NULLS FIRST,id) WHERE state<>'stopped';
ALTER TABLE close_goals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON close_goals USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
-- An assistant card can hand its close to the agent; due_on is the deadline the card proposes.
ALTER TABLE chat_messages DROP CONSTRAINT chat_messages_action_check;
ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_action_check CHECK (action IN ('prepare','recheck','stop','delegate'));
ALTER TABLE chat_messages ADD COLUMN due_on date;
COMMIT;
