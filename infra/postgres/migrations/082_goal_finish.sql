BEGIN;
-- A goal's finish line: the close's documents ready for review, or the close signed off. ready_at is when a sign-off
-- goal's documents last all became ready for review; a card proposes its finish line like its deadline.
ALTER TABLE close_goals ADD COLUMN finish text NOT NULL DEFAULT 'review' CHECK (finish IN ('review','signoff'));
ALTER TABLE close_goals ADD COLUMN ready_at timestamptz;
ALTER TABLE chat_messages ADD COLUMN finish text CHECK (finish IN ('review','signoff'));
COMMIT;
