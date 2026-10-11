BEGIN;
-- A client's books file sent through a request for the month's books (docs/books-sources.md, Client-provided books) is
-- released as one of the client's files for that month, not as evidence: import_id is the file it was kept as. Deleting
-- the file deletes the attachment holding the same bytes, so none of them is kept.
ALTER TABLE client_attachments ADD COLUMN import_id bigint REFERENCES books_imports(id) ON DELETE CASCADE;
COMMIT;
