BEGIN;
-- An executive's conversation is its own: a member whose access later changes to executive access never sees, and the
-- model is never replayed, the firm-work turns they had before (docs/agent-chat.md, "Executive view").
ALTER TABLE chat_messages ADD COLUMN executive boolean NOT NULL DEFAULT false;
COMMIT;
