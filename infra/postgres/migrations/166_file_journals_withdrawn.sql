BEGIN;
-- A written-back journal holds its month's sign-off until it is verified (docs/books-sources.md, Writing back). One that
-- was never imported into the client's ledger is withdrawn instead, by a person who says why; its source can then be
-- exported again.
ALTER TABLE file_journals DROP CONSTRAINT file_journals_state_check,
 ADD CONSTRAINT file_journals_state_check CHECK (state IN ('exported','entered','verified','uncertain','withdrawn')),
 ADD COLUMN withdrawn_by text NOT NULL DEFAULT '',
 ADD COLUMN withdrawn_at timestamptz,
 ADD COLUMN withdrawn_reason text NOT NULL DEFAULT '' CHECK (length(withdrawn_reason) <= 500 AND ((state='withdrawn')=(withdrawn_reason<>'')));
DROP INDEX file_journals_once;
CREATE UNIQUE INDEX file_journals_once ON file_journals(close_id, source_key) WHERE state NOT IN ('uncertain','withdrawn');
COMMIT;
