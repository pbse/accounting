BEGIN;
-- The exceptions each capture found, so later captures can show what is new since a review.
ALTER TABLE quickbooks_entity_syncs ADD COLUMN exceptions jsonb;
-- The capture an accountant last reviewed for each close.
CREATE TABLE daily_reviews (
 close_id bigint PRIMARY KEY REFERENCES client_closes(id),
 sync_id bigint NOT NULL REFERENCES quickbooks_entity_syncs(id),
 reviewed_by text NOT NULL,
 reviewed_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE daily_reviews ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON daily_reviews USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
