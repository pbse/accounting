BEGIN;
ALTER TABLE workpapers DROP CONSTRAINT workpapers_kind_check;
ALTER TABLE workpapers ADD CONSTRAINT workpapers_kind_check CHECK(kind IN ('prepaid','reconciliation','accrual','settlement','loan'));
ALTER TABLE procedure_versions DROP CONSTRAINT procedure_versions_kind_check;
ALTER TABLE procedure_versions ADD CONSTRAINT procedure_versions_kind_check CHECK(kind IN ('prepaid','reconciliation','accrual','loan'));
COMMIT;
