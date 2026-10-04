BEGIN;
ALTER TABLE procedure_drafts DROP CONSTRAINT procedure_drafts_engine_check;
ALTER TABLE procedure_drafts ADD CONSTRAINT procedure_drafts_engine_check CHECK(engine IN ('local','openai','context_machine'));
COMMIT;
