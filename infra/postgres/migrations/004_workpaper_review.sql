BEGIN;
ALTER TABLE evidence ADD CONSTRAINT evidence_close_identity UNIQUE(close_id,id);
ALTER TABLE evidence ADD COLUMN superseded_by bigint;
ALTER TABLE evidence ADD COLUMN replacement_reason text;
ALTER TABLE evidence ADD COLUMN replaced_by text;
ALTER TABLE evidence ADD COLUMN replaced_at timestamptz;
ALTER TABLE evidence ADD CONSTRAINT evidence_revision_order CHECK(superseded_by>id);
ALTER TABLE evidence ADD CONSTRAINT evidence_replacement_scope FOREIGN KEY(close_id,superseded_by) REFERENCES evidence(close_id,id);
CREATE TABLE workpaper_reviews (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 paper_digest text NOT NULL,
 decision text NOT NULL CHECK(decision IN ('approved','changes_requested')),
 note text NOT NULL CHECK(length(note) BETWEEN 1 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workpaper_reviews_latest ON workpaper_reviews(workpaper_id,id DESC);
CREATE VIEW workpaper_review_state AS
 SELECT w.*,COALESCE(r.id::text,'') AS review_id,
 CASE WHEN EXISTS (
   SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(w.paper->'sources','null'::jsonb),'[]'::jsonb)) s
   LEFT JOIN evidence e ON e.id::text=s->>'id' AND e.close_id=w.close_id
   WHERE e.id IS NULL OR e.digest IS DISTINCT FROM s->>'digest' OR e.superseded_by IS NOT NULL
 ) THEN 'source_changed' ELSE COALESCE(r.decision,'pending') END AS review_state
 FROM workpapers w LEFT JOIN LATERAL (
   SELECT id,decision FROM workpaper_reviews WHERE workpaper_id=w.id ORDER BY id DESC LIMIT 1
 ) r ON true;
COMMIT;
