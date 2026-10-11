BEGIN;
-- A published management pack carries the reviewed, client-safe explanation of each line it explains (material
-- movements first), frozen with its figures; a changed explanation is a new version naming the one it corrects.
ALTER TABLE management_publications ADD COLUMN explanations jsonb NOT NULL DEFAULT '[]'::jsonb;
COMMIT;
