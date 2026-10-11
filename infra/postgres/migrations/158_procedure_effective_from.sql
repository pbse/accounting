BEGIN;
-- A procedure version may take effect from a month (YYYY-MM): a close of an earlier month keeps the version in effect
-- for it, so a method change made mid-year does not rewrite the months before it. NULL is in effect for every month,
-- as every version before this was.
ALTER TABLE procedure_versions ADD COLUMN effective_from text CHECK (effective_from ~ '^[0-9]{4}-(0[1-9]|1[0-2])$');
COMMIT;
