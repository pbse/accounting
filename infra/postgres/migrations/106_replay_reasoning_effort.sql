BEGIN;
-- NULL preserves unknown historical effort; new runs explicitly record their configured value.
-- Effort changes require a new replay even when the model ID stays the same.
ALTER TABLE replay_runs ADD COLUMN reasoning_effort text;
COMMIT;
