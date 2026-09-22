BEGIN;
ALTER TABLE evidence DROP CONSTRAINT evidence_content_check;
ALTER TABLE evidence ADD COLUMN original bytea;
ALTER TABLE evidence ADD COLUMN extraction_state text NOT NULL DEFAULT 'ready' CHECK(extraction_state IN ('queued','processing','ready','failed'));
ALTER TABLE evidence ADD COLUMN extraction_error text NOT NULL DEFAULT '';
ALTER TABLE evidence ADD COLUMN extraction_claim text;
ALTER TABLE evidence ADD COLUMN extraction_started_at timestamptz;
ALTER TABLE evidence ADD CONSTRAINT evidence_content_bounds CHECK(octet_length(content)<=524288 AND (original IS NOT NULL OR octet_length(content)>0));
ALTER TABLE evidence ADD CONSTRAINT evidence_original_bounds CHECK(original IS NULL OR octet_length(original) BETWEEN 1 AND 5242880);
CREATE INDEX evidence_extraction_pending ON evidence(id) WHERE extraction_state IN ('queued','processing');
CREATE OR REPLACE VIEW workpaper_review_state AS
 SELECT w.*,COALESCE(r.id::text,'') AS review_id,
 CASE WHEN EXISTS (SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text) OR EXISTS (
   SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(w.paper->'sources','null'::jsonb),'[]'::jsonb)) s
   LEFT JOIN evidence e ON e.id::text=s->>'id' AND e.close_id=w.close_id
   WHERE e.id IS NULL OR e.digest IS DISTINCT FROM s->>'digest' OR e.superseded_by IS NOT NULL
 ) THEN 'source_changed' ELSE COALESCE(r.decision,'pending') END AS review_state
 FROM workpapers w LEFT JOIN LATERAL (
   SELECT id,decision FROM workpaper_reviews WHERE workpaper_id=w.id ORDER BY id DESC LIMIT 1
 ) r ON true;
COMMIT;
