BEGIN;
-- Whether a hand-over card came from a request that asked for every month, so the card offers the monthly hand-over ticked.
ALTER TABLE chat_messages ADD COLUMN every_month boolean NOT NULL DEFAULT false;
COMMIT;
