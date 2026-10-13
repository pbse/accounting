BEGIN;
-- A payroll prepared straight through uses the accounts of the client's last approved payroll from the same provider and
-- records it as payroll_precedent, as a carried schedule records its parent: once that payroll's approval no longer stands
-- as it was, the draft's review reopens (source_changed). The view keeps applying each firm's row-level security.
CREATE OR REPLACE VIEW workpaper_review_state WITH (security_invoker=true) AS
WITH RECURSIVE lineage(child_id,parent_id,expected_digest,expected_review_id) AS (
 SELECT id,COALESCE((paper->'carry_forward'->>'paper_id')::bigint,(paper->'recurring_accrual'->>'paper_id')::bigint,(paper->'payroll_precedent'->>'paper_id')::bigint),
 COALESCE(paper->'carry_forward'->>'digest',paper->'recurring_accrual'->>'digest',paper->'payroll_precedent'->>'digest'),
 COALESCE(paper->'carry_forward'->>'review_id',paper->'recurring_accrual'->>'review_id',paper->'payroll_precedent'->>'review_id')
 FROM workpapers WHERE paper->'carry_forward'->>'paper_id' IS NOT NULL OR paper->'recurring_accrual'->>'paper_id' IS NOT NULL OR paper->'payroll_precedent'->>'paper_id' IS NOT NULL
 UNION
 SELECT l.child_id,COALESCE((p.paper->'carry_forward'->>'paper_id')::bigint,(p.paper->'recurring_accrual'->>'paper_id')::bigint,(p.paper->'payroll_precedent'->>'paper_id')::bigint),
 COALESCE(p.paper->'carry_forward'->>'digest',p.paper->'recurring_accrual'->>'digest',p.paper->'payroll_precedent'->>'digest'),
 COALESCE(p.paper->'carry_forward'->>'review_id',p.paper->'recurring_accrual'->>'review_id',p.paper->'payroll_precedent'->>'review_id')
 FROM lineage l JOIN workpapers p ON p.id=l.parent_id
 WHERE p.paper->'carry_forward'->>'paper_id' IS NOT NULL OR p.paper->'recurring_accrual'->>'paper_id' IS NOT NULL OR p.paper->'payroll_precedent'->>'paper_id' IS NOT NULL
)
SELECT w.id,w.close_id,w.kind,w.digest,w.paper,w.created_at,w.review_id,
 CASE WHEN EXISTS (
 SELECT 1 FROM lineage l LEFT JOIN workpaper_source_review_state p ON p.id=l.parent_id
 WHERE l.child_id=w.id AND (p.id IS NULL OR p.review_state<>'approved'
 OR p.digest IS DISTINCT FROM l.expected_digest OR p.review_id IS DISTINCT FROM l.expected_review_id)
 ) THEN 'source_changed' ELSE w.review_state END AS review_state
FROM workpaper_source_review_state w;
COMMIT;
