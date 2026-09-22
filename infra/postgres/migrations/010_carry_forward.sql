BEGIN;
-- Keep the existing same-close source/review rules and add predecessor freshness.
ALTER VIEW workpaper_review_state RENAME TO workpaper_source_review_state;
CREATE VIEW workpaper_review_state AS
-- ponytail: compute lineage live; materialize only if measured portfolio latency requires it.
WITH RECURSIVE lineage(child_id,parent_id,expected_digest,expected_review_id) AS (
 SELECT id,(paper->'carry_forward'->>'paper_id')::bigint,
 paper->'carry_forward'->>'digest',paper->'carry_forward'->>'review_id'
 FROM workpapers WHERE paper->'carry_forward'->>'paper_id' IS NOT NULL
 UNION
 SELECT l.child_id,(p.paper->'carry_forward'->>'paper_id')::bigint,
 p.paper->'carry_forward'->>'digest',p.paper->'carry_forward'->>'review_id'
 FROM lineage l JOIN workpapers p ON p.id=l.parent_id
 WHERE p.paper->'carry_forward'->>'paper_id' IS NOT NULL
)
SELECT w.id,w.close_id,w.kind,w.digest,w.paper,w.created_at,w.review_id,
 CASE WHEN EXISTS (
 SELECT 1 FROM lineage l LEFT JOIN workpaper_source_review_state p ON p.id=l.parent_id
 WHERE l.child_id=w.id AND (p.id IS NULL OR p.review_state<>'approved'
 OR p.digest IS DISTINCT FROM l.expected_digest OR p.review_id IS DISTINCT FROM l.expected_review_id)
 ) THEN 'source_changed' ELSE w.review_state END AS review_state
FROM workpaper_source_review_state w;
COMMIT;
