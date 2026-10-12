BEGIN;
-- A client asked to connect its books (docs/books-sources.md, Client-provided books) signs in to QuickBooks or Xero
-- through the request's private link: the attempt is bound to that request (request_id) and to the link it was started
-- from (request_link, the link's digest), and ends with that link; the connection it makes waits for a member's
-- confirmation like any other.
ALTER TABLE quickbooks_connections ADD COLUMN request_id bigint REFERENCES work_requests(id) ON DELETE SET NULL, ADD COLUMN request_link text;
ALTER TABLE xero_connections ADD COLUMN request_id bigint REFERENCES work_requests(id) ON DELETE SET NULL, ADD COLUMN request_link text;
COMMIT;
