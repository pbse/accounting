BEGIN;
-- A grant may also authorize an approved Stripe settlement's fee entry.
ALTER TABLE production_posting_grants DROP CONSTRAINT production_posting_grants_kinds;
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_kinds CHECK (cardinality(kinds) >= 1 AND kinds <@ ARRAY['prepaid','accrual','loan','owner','settlement']);
COMMIT;
