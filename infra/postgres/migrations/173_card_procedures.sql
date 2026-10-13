BEGIN;
-- A client's reconciliation procedure may approve card accounts too: a credit-card reconciliation is then prepared
-- without a person confirming the agent's reading when the statement and the ledger tie under the card liability method
-- (card_liability_month_v1). Without it a card waits for a person, as before.
ALTER TABLE procedure_versions ADD COLUMN covers_cards boolean NOT NULL DEFAULT false;
ALTER TABLE procedure_versions ADD CONSTRAINT procedure_versions_cards_reconciliation CHECK (NOT covers_cards OR kind='reconciliation');
COMMIT;
