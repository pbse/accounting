BEGIN;
-- The findings each Xero snapshot's transactions raised, recorded when it is saved, so Daily books can show what is new.
ALTER TABLE xero_snapshots ADD COLUMN exceptions jsonb;
-- A close's last review is of a QuickBooks capture or a Xero snapshot.
ALTER TABLE daily_reviews ALTER COLUMN sync_id DROP NOT NULL;
ALTER TABLE daily_reviews ADD COLUMN xero_snapshot_id bigint REFERENCES xero_snapshots(id);
ALTER TABLE daily_reviews ADD CONSTRAINT daily_reviews_one_source CHECK ((sync_id IS NULL) <> (xero_snapshot_id IS NULL));
COMMIT;
