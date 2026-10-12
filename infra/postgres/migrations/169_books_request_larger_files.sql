BEGIN;
-- A client's books file sent through a request for the month's books may be as large as one a member sends, 50 MiB
-- (docs/books-sources.md, Client-provided books); documents keep their own smaller limits, checked when they arrive.
ALTER TABLE client_attachments DROP CONSTRAINT client_attachments_body_check,
 ADD CONSTRAINT client_attachments_body_check CHECK (body IS NULL OR octet_length(body) BETWEEN 1 AND 52428800);
COMMIT;
