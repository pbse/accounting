BEGIN;
-- One account change on a line of a Bill, an expense (Purchase) or an income deposit, under an accountant's rule.
ALTER TABLE bill_corrections ADD COLUMN entity text NOT NULL DEFAULT 'Bill' CHECK (entity IN ('Bill','Purchase','Deposit'));
ALTER TABLE bill_corrections ADD COLUMN rule_reference text NOT NULL DEFAULT '';
ALTER TABLE bill_corrections DROP CONSTRAINT bill_corrections_realm_bill_id_key;
ALTER TABLE bill_corrections ADD CONSTRAINT bill_corrections_realm_entity_key UNIQUE (realm,entity,bill_id);
ALTER TABLE bill_corrections DROP CONSTRAINT bill_corrections_treatment_check;
ALTER TABLE bill_corrections ADD CONSTRAINT bill_corrections_treatment_check CHECK (treatment IN ('uncategorized_expense','inconsistent_coding','uncategorized_income'));
-- A grant may also authorize expense and income-deposit line recodes; each stays off until the operator enables it.
ALTER TABLE production_posting_grants DROP CONSTRAINT production_posting_grants_kinds;
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_kinds CHECK (cardinality(kinds) >= 1 AND kinds <@ ARRAY['prepaid','accrual','loan','owner','settlement','bill_correction','vendor_credit','purchase_correction','deposit_correction']);
COMMIT;
