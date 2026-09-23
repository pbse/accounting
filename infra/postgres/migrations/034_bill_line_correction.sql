BEGIN;
ALTER TABLE bill_corrections ADD COLUMN target_line_id text;
UPDATE bill_corrections SET target_line_id=before_bill->'Line'->0->>'Id';
ALTER TABLE bill_corrections ALTER COLUMN target_line_id SET NOT NULL;
COMMIT;
