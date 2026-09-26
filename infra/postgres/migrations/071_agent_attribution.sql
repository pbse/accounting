BEGIN;
-- The MCP client that made a record through a member's approved agent access; the member stays its author.
ALTER TABLE evidence ADD COLUMN agent text;
ALTER TABLE preparation_jobs ADD COLUMN agent text;
ALTER TABLE close_preparations ADD COLUMN agent text;
ALTER TABLE request_drafts ADD COLUMN agent text;
ALTER TABLE approval_requests ADD COLUMN agent text;
COMMIT;
