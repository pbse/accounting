BEGIN;
-- The state, as review reads it, of the drafts a request's wording came from when the request was approved. A request
-- takes its first reply only while it is unchanged; other evidence the close gains no longer matters. A request
-- approved from drafts before this migration did not record their state, so it stays unanswerable until reissued.
ALTER TABLE work_requests ADD COLUMN approved_papers jsonb DEFAULT '[]';
UPDATE work_requests q SET approved_papers=NULL FROM request_drafts d WHERE d.id=q.draft_id AND d.workpaper_ids<>'[]'::jsonb;
COMMIT;
