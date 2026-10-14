BEGIN;
-- What a close goal's drafted client request covers, as it was drafted: its questions (each with what an answer lets
-- the agent continue), the ledger accounts it asks the client's records for (with the capture they were read from),
-- the drafts whose missing pages it asks for, and what it left with the firm. Approval checks the request still
-- covers exactly this; withdrawn marks a draft whose questions were all answered or overtaken before approval.
ALTER TABLE request_drafts ADD COLUMN proposal jsonb;
COMMIT;
