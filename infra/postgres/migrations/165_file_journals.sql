BEGIN;
-- Writing back by file (docs/books-sources.md, Writing back; delivery step 10): for a month whose books are its files,
-- the approved drafts' month-end entries are downloaded as a journal import batch for the client's ledger, each journal
-- with a reference in its memo, so a duplicate or partial import can be seen. Each journal moves from exported to entered
-- (a person records that it was imported, with the ledger's own journal reference), then verified or uncertain.
CREATE TABLE file_journals (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 -- batch numbers the close's downloads; reference is the journal's own, written in its memo.
 batch integer NOT NULL CHECK (batch >= 1),
 reference text NOT NULL CHECK (length(reference) BETWEEN 1 AND 60),
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 -- paper_digest is the draft's digest when it was exported: a draft revised since is not this journal.
 paper_digest text NOT NULL,
 lines jsonb NOT NULL CHECK (jsonb_typeof(lines)='array'),
 state text NOT NULL DEFAULT 'exported' CHECK (state IN ('exported','entered','verified','uncertain')),
 ledger_reference text NOT NULL DEFAULT '' CHECK (length(ledger_reference) <= 120),
 exported_by text NOT NULL,
 exported_at timestamptz NOT NULL DEFAULT now(),
 entered_by text NOT NULL DEFAULT '',
 entered_at timestamptz,
 UNIQUE (close_id, reference)
);
-- A draft's entries are in one journal at a time, unless that one was found uncertain.
CREATE UNIQUE INDEX file_journals_once ON file_journals(close_id, workpaper_id) WHERE state <> 'uncertain';
ALTER TABLE file_journals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON file_journals USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
