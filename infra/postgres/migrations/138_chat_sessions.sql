BEGIN;
-- A member's conversations with the agent are many: a chat is a session with a title taken from its first question,
-- kept in a scope (firm-wide, one close, or an executive's), continued while it is less than 12 hours quiet, and
-- otherwise left behind for a new one (docs/agent-chat.md, "Sessions"). Deleting a session takes its messages.
CREATE TABLE chat_sessions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
 author text NOT NULL,
 close_id bigint REFERENCES client_closes(id) ON DELETE CASCADE,
 executive boolean NOT NULL DEFAULT false,
 title text NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
 -- renamed says the member chose the title; otherwise it follows the chat's first question, even once that question
 -- was pruned (a lost client's turns leave no trace in the list).
 renamed boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 last_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX chat_sessions_recent ON chat_sessions(organization_id,author,last_at DESC);
ALTER TABLE chat_sessions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON chat_sessions USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE chat_messages ADD COLUMN session_id bigint REFERENCES chat_sessions(id) ON DELETE CASCADE;
-- Every conversation so far becomes one session, titled by its first question.
INSERT INTO chat_sessions(organization_id,author,close_id,executive,title,created_at,last_at)
SELECT m.organization_id,m.author,m.close_id,m.executive,
 COALESCE(NULLIF(left(regexp_replace((SELECT u.body FROM chat_messages u WHERE u.organization_id=m.organization_id AND u.author=m.author AND u.close_id IS NOT DISTINCT FROM m.close_id AND u.executive=m.executive AND u.role='user' ORDER BY u.id LIMIT 1),'\s+',' ','g'),80),''),'Conversation'),
 min(m.created_at),max(m.created_at)
FROM chat_messages m GROUP BY m.organization_id,m.author,m.close_id,m.executive;
UPDATE chat_messages m SET session_id=s.id FROM chat_sessions s WHERE s.organization_id=m.organization_id AND s.author=m.author AND s.close_id IS NOT DISTINCT FROM m.close_id AND s.executive=m.executive;
ALTER TABLE chat_messages ALTER COLUMN session_id SET NOT NULL;
CREATE INDEX chat_messages_session ON chat_messages(session_id,id);
-- A message inserted without a session (an older path, a fixture) joins its scope's current session, or opens one
-- named by the message, exactly as the chat route does; the session's last activity follows its newest message.
CREATE FUNCTION chat_message_session() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.session_id IS NULL THEN
  SELECT id INTO NEW.session_id FROM chat_sessions WHERE organization_id=NEW.organization_id AND author=NEW.author AND close_id IS NOT DISTINCT FROM NEW.close_id AND executive=NEW.executive AND last_at>NEW.created_at-interval '12 hours' AND last_at<=NEW.created_at+interval '12 hours' ORDER BY last_at DESC LIMIT 1;
  IF NEW.session_id IS NULL THEN
   INSERT INTO chat_sessions(organization_id,author,close_id,executive,title,created_at,last_at)
   VALUES(NEW.organization_id,NEW.author,NEW.close_id,NEW.executive,COALESCE(NULLIF(left(regexp_replace(NEW.body,'\s+',' ','g'),80),''),'Conversation'),NEW.created_at,NEW.created_at)
   RETURNING id INTO NEW.session_id;
  END IF;
 END IF;
 UPDATE chat_sessions SET last_at=GREATEST(last_at,NEW.created_at) WHERE id=NEW.session_id;
 RETURN NEW;
END $$;
CREATE TRIGGER chat_message_session BEFORE INSERT ON chat_messages FOR EACH ROW EXECUTE FUNCTION chat_message_session();
COMMIT;
