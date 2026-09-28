-- A plan template with no firm is shared: every firm may read it, to copy it into a draft of its own
-- (docs/workflow-engine.md#lifecycle). Only reading is added; writes stay with the plan's own firm.
CREATE POLICY shared_templates ON workflow_plans FOR SELECT USING (organization_id IS NULL AND is_template);
