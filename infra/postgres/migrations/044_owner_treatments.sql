BEGIN;
ALTER TABLE workpapers DROP CONSTRAINT workpapers_kind_check;
ALTER TABLE workpapers ADD CONSTRAINT workpapers_kind_check CHECK(kind IN ('prepaid','reconciliation','accrual','settlement','loan','owner'));
-- A client's owner procedure records its entity type; the firm default carries only policy and account names.
ALTER TABLE procedure_versions DROP CONSTRAINT procedure_versions_kind_check;
ALTER TABLE procedure_versions ADD CONSTRAINT procedure_versions_kind_check CHECK(kind IN ('prepaid','reconciliation','accrual','loan','basis','owner'));
COMMIT;
