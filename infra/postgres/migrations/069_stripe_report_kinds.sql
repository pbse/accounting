BEGIN;
-- A close can capture the Payouts and balance activity reports as well as the payout reconciliation, one of each in flight.
ALTER TABLE stripe_report_runs ADD COLUMN report text NOT NULL DEFAULT 'payout_reconciliation' CHECK (report IN ('payout_reconciliation','payouts','activity'));
DROP INDEX stripe_report_in_flight;
CREATE UNIQUE INDEX stripe_report_in_flight ON stripe_report_runs(close_id,period,report) WHERE state IN ('requesting','pending');
COMMIT;
