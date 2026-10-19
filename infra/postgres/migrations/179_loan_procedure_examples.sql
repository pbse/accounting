BEGIN;
ALTER TABLE procedure_drafts DROP CONSTRAINT procedure_drafts_kind_check;
ALTER TABLE procedure_drafts ADD CONSTRAINT procedure_drafts_kind_check CHECK(kind IN ('prepaid','accrual','loan'));
COMMIT;
