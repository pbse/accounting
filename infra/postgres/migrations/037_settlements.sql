BEGIN;
ALTER TABLE workpapers DROP CONSTRAINT workpapers_kind_check;
ALTER TABLE workpapers ADD CONSTRAINT workpapers_kind_check CHECK(kind IN ('prepaid','reconciliation','accrual','settlement'));
COMMIT;
