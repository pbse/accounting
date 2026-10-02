BEGIN;
-- Our own team's setup or support help on a close, recorded by hand beside the firm's off-screen time and reported
-- apart from it.
ALTER TABLE close_effort_entries DROP CONSTRAINT close_effort_entries_activity_check;
ALTER TABLE close_effort_entries ADD CONSTRAINT close_effort_entries_activity_check CHECK (activity IN ('prepare','review','evidence','requests','signoff','configuration','assistance'));
COMMIT;
