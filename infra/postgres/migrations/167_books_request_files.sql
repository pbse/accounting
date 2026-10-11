BEGIN;
-- A client's books file sent through a request for the month's books (docs/books-sources.md, Client-provided books) is
-- released as one of the client's files for that month, not as evidence: import_id is the file it was kept as. Deleting
-- that file removes the attachment's bytes too (the deletion route clears body first); the attachment keeps its request,
-- scan and release, without its content.
ALTER TABLE client_attachments ADD COLUMN import_id bigint REFERENCES books_imports(id) ON DELETE SET NULL,
 ALTER COLUMN body DROP NOT NULL,
 DROP CONSTRAINT client_attachments_body_check,
 ADD CONSTRAINT client_attachments_body_check CHECK (body IS NULL OR octet_length(body) BETWEEN 1 AND 5242880);
COMMIT;
