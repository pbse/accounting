BEGIN;
-- A client's ledger connection belongs to the client, not to a month (docs/books-sources.md, Connections): one made
-- from the client's page names no close. A connection made from a month before this keeps naming it, which only says
-- where it was made; every reader matches a connection by firm and client, as before.
ALTER TABLE quickbooks_connections ALTER COLUMN close_id DROP NOT NULL;
ALTER TABLE xero_connections ALTER COLUMN close_id DROP NOT NULL;
COMMIT;
