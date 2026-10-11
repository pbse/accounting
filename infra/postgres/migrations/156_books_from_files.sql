BEGIN;
-- Months on files (docs/books-sources.md, One authoritative books version per month; delivery step 9, first part): a
-- client's default source, or one month's, can be its files. A month whose source is files reads the books version of
-- its file's current acceptance (source files, source_id the acceptance), written by the worker and on each acceptance
-- or choice of source. Its activity can be unavailable (the file gives none): activity_unavailable says why, so a
-- reader never takes no activity for zero.
ALTER TABLE books_sources DROP CONSTRAINT books_sources_source_check;
ALTER TABLE books_sources ADD CONSTRAINT books_sources_source_check CHECK (source IN ('newest','quickbooks','xero','files'));
ALTER TABLE books_versions ADD COLUMN activity_unavailable text NOT NULL DEFAULT '';
COMMIT;
