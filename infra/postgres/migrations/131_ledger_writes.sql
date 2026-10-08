BEGIN;
-- Whether the firm lets approved entries be written to its clients' live books: QuickBooks production companies and
-- every Xero organisation. Off until a firm administrator turns it on in Firm settings, with a reason; until then a
-- person books each approved entry. QuickBooks sandbox companies are outside it.
ALTER TABLE organizations ADD COLUMN allow_ledger_writes boolean NOT NULL DEFAULT false;
ALTER TABLE organization_policy_changes ADD COLUMN ledger_writes boolean;
COMMIT;
