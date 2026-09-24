BEGIN;
-- A basis procedure records the client's accounting basis (method cash, modified_cash or accrual).
ALTER TABLE procedure_versions DROP CONSTRAINT procedure_versions_kind_check;
ALTER TABLE procedure_versions ADD CONSTRAINT procedure_versions_kind_check CHECK(kind IN ('prepaid','reconciliation','accrual','loan','basis'));
COMMIT;
