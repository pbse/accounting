BEGIN;
-- A close's goal drafts one consolidated client request for the open questions its documents raise: the draft names
-- the jobs it covers, each at the reading it was drafted from, and the recipient it proposes (the client's most recently
-- approved one). A person approves it from the goal card, chat or the Requests tab.
ALTER TABLE request_drafts ADD COLUMN job_ids jsonb, ADD COLUMN recipient text;
COMMIT;
