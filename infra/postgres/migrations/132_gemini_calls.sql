BEGIN;
-- Gemini's PDF page readings count against the firm's one daily model-call limit, with the rest of its model calls, and
-- are also counted apart so the security view attributes each call to the provider that received it.
ALTER TABLE model_calls ADD COLUMN gemini_calls integer NOT NULL DEFAULT 0 CHECK (gemini_calls >= 0 AND gemini_calls <= calls);
COMMIT;
