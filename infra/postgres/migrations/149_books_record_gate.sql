BEGIN;
-- books_record_gate: how far the month-books record is verified for each reader build (docs/books-sources.md, Migration
-- step 3; delivery step 5): the last version the latest finished parity run of that build checked, and whether it found
-- every version agreeing. The parity worker writes it as each run finishes; a reader switched onto the record reads a
-- version only up to it, and none while its run found a disagreement. It names no firm and no capture, so every
-- session reads it; only system work writes it.
CREATE TABLE books_record_gate (
 build text PRIMARY KEY,
 -- run is the parity run that decided it: only a later run replaces it.
 run bigint NOT NULL,
 through bigint NOT NULL,
 clean boolean NOT NULL,
 finished_at timestamptz NOT NULL
);
ALTER TABLE books_record_gate ENABLE ROW LEVEL SECURITY;
CREATE POLICY anyone_reads ON books_record_gate FOR SELECT USING (true);
CREATE POLICY system_writes ON books_record_gate USING (current_setting('app.organization',true)='*') WITH CHECK (current_setting('app.organization',true)='*');
COMMIT;
