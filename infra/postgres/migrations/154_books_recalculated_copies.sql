BEGIN;
-- A recalculated copy (docs/books-sources.md, Formulas; delivery step 6): a workbook's formula amounts are the last
-- calculation, not necessarily the current one, and the reader does not recalculate them, so they hold the file's use as
-- the month's books. A values-only export from the ledger has none. For a workbook the firm itself maintains, a reviewer
-- attests with the file's scope that it is a copy made in a spreadsheet application after every linked workbook was
-- refreshed from its confirmed version and the whole workbook recalculated, its input cells matching the original's;
-- that releases its formula amounts.
ALTER TABLE books_import_scopes ADD COLUMN recalculated boolean NOT NULL DEFAULT false;
COMMIT;
