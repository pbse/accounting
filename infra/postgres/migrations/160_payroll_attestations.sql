BEGIN;
-- A payroll recording is approved only with its reviewer's two confirmations (the provider's final, approved summary
-- for the period; not already booked), recorded on the approving review with the exact summary and ledger evidence it
-- was given against; the review's paper_digest already binds the draft version.
ALTER TABLE workpaper_reviews ADD COLUMN attestations jsonb;
COMMIT;
