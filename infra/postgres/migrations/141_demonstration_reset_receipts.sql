BEGIN;
-- A demonstration's removal receipts each close it deletes, as the deletion route does, under its own reason, so the
-- roster's owed months never count a demonstration month as a real client's finished work.
ALTER TABLE close_deletion_receipts DROP CONSTRAINT IF EXISTS close_deletion_receipts_reason_code_check;
ALTER TABLE close_deletion_receipts ADD CONSTRAINT close_deletion_receipts_reason_code_check CHECK (reason_code IN ('retention_expired','client_request','contract_termination','demonstration_reset'));
COMMIT;
