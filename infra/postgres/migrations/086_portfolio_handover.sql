BEGIN;
-- A portfolio goal can hand its closes to the agent (delegate) until each is ready for review or signed off (finish);
-- each close's deadline for the agent (due_on) is fixed when the goal is previewed.
ALTER TABLE portfolio_operations DROP CONSTRAINT portfolio_operations_action_check;
ALTER TABLE portfolio_operations ADD CONSTRAINT portfolio_operations_action_check CHECK (action IN ('prepare','recheck','stop','delegate'));
ALTER TABLE portfolio_operations ADD COLUMN finish text CHECK (finish IN ('review','signoff'));
ALTER TABLE portfolio_operations ADD CONSTRAINT portfolio_operations_finish CHECK ((action='delegate')=(finish IS NOT NULL));
ALTER TABLE portfolio_items ADD COLUMN due_on date;
COMMIT;
