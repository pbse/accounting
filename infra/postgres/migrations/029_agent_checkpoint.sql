BEGIN;
ALTER TABLE preparation_intakes ADD COLUMN agent_checkpoint jsonb NOT NULL DEFAULT '{}'::jsonb;
COMMIT;
