BEGIN;
-- An approval needs no note: the review records who approved which exact version and when. Returning a draft for
-- changes still says why (1–2,000 bytes).
ALTER TABLE workpaper_reviews DROP CONSTRAINT workpaper_reviews_note_check,
 ADD CONSTRAINT workpaper_reviews_note_check CHECK (length(note) <= 2000 AND (decision = 'approved' OR length(note) >= 1));
COMMIT;
