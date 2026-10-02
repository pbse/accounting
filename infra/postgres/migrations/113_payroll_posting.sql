BEGIN;
ALTER TABLE production_posting_grants DROP CONSTRAINT production_posting_grants_kinds;
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_kinds CHECK (cardinality(kinds) >= 1 AND kinds <@ ARRAY['prepaid','accrual','loan','owner','payroll','settlement','bill_correction','vendor_credit','purchase_correction','deposit_correction','payment_application','credit_application']);
COMMIT;
