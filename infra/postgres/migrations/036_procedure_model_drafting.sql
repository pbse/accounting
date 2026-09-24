BEGIN;
ALTER TABLE procedure_drafts ADD COLUMN engine text NOT NULL DEFAULT 'local' CHECK(engine IN ('local','openai'));
ALTER TABLE procedure_drafts ADD COLUMN model text NOT NULL DEFAULT '';
COMMIT;
