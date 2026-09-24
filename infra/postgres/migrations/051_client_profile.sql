BEGIN;
-- The client's home currency and first fiscal month as the connected ledger reports them.
ALTER TABLE quickbooks_connections ADD COLUMN home_currency text CHECK(home_currency ~ '^[A-Z]{3}$'), ADD COLUMN fiscal_year_start int CHECK(fiscal_year_start BETWEEN 1 AND 12);
ALTER TABLE xero_connections ADD COLUMN home_currency text CHECK(home_currency ~ '^[A-Z]{3}$'), ADD COLUMN fiscal_year_start int CHECK(fiscal_year_start BETWEEN 1 AND 12);
COMMIT;
