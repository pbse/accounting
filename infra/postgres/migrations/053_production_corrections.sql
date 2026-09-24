BEGIN;
-- A grant may also authorize Bill expense corrections and supplier credits in a client's production company.
ALTER TABLE production_posting_grants DROP CONSTRAINT production_posting_grants_kinds;
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_kinds CHECK (cardinality(kinds) >= 1 AND kinds <@ ARRAY['prepaid','accrual','loan','owner','settlement','bill_correction','vendor_credit']);
-- The grant a production correction or credit was previewed under; sandbox attempts have none.
ALTER TABLE bill_corrections ADD COLUMN grant_id bigint REFERENCES production_posting_grants(id);
ALTER TABLE vendor_credit_postings ADD COLUMN grant_id bigint REFERENCES production_posting_grants(id);
COMMIT;
