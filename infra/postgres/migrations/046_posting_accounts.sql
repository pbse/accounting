BEGIN;
-- A production grant lists the QuickBooks accounts and the draft kinds it authorizes.
-- Existing grants keep exactly their prepaid authority; other kinds need a new grant.
ALTER TABLE production_posting_grants ADD COLUMN account_ids text[];
ALTER TABLE production_posting_grants ADD COLUMN kinds text[] NOT NULL DEFAULT '{prepaid}';
UPDATE production_posting_grants SET account_ids=ARRAY[prepaid_account_id,expense_account_id];
ALTER TABLE production_posting_grants ALTER COLUMN account_ids SET NOT NULL;
ALTER TABLE production_posting_grants ALTER COLUMN kinds DROP DEFAULT;
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_accounts CHECK (cardinality(account_ids) BETWEEN 2 AND 20);
ALTER TABLE production_posting_grants ADD CONSTRAINT production_posting_grants_kinds CHECK (cardinality(kinds) >= 1 AND kinds <@ ARRAY['prepaid','accrual','loan','owner']);
ALTER TABLE production_posting_grants DROP COLUMN prepaid_account_id, DROP COLUMN expense_account_id;
-- Saved intents and receipts move from the prepaid/expense pair to an account list.
UPDATE production_postings SET ledger_before=jsonb_strip_nulls(jsonb_build_object('company',ledger_before->'company','closed_through',ledger_before->'closed_through','accounts',jsonb_build_array((ledger_before->'prepaid')||'{"classification":"Asset"}',(ledger_before->'expense')||'{"classification":"Expense"}'))) WHERE ledger_before ? 'prepaid';
UPDATE production_postings SET ledger_after=jsonb_set(ledger_after,'{accounts}',jsonb_strip_nulls(jsonb_build_object('company',ledger_after->'accounts'->'company','accounts',jsonb_build_array((ledger_after->'accounts'->'prepaid')||'{"classification":"Asset"}',(ledger_after->'accounts'->'expense')||'{"classification":"Expense"}')))) WHERE ledger_after->'accounts' ? 'prepaid';
COMMIT;
