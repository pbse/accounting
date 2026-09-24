BEGIN;
-- A member requests agent (MCP) access from their own session; a firm administrator approves it.
-- An approval counts only while member_access_id is still the member's current membership record.
CREATE TABLE agent_access (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL CHECK (left(subject,5)='user_' AND length(subject) BETWEEN 6 AND 200),
 state text NOT NULL CHECK (state IN ('requested','approved','declined','revoked')),
 scope text NOT NULL CHECK (scope IN ('read','prepare')),
 member_access_id bigint NOT NULL REFERENCES organization_member_access(id),
 reason text NOT NULL CHECK (length(reason) BETWEEN 10 AND 2000),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE VIEW current_agent_access AS SELECT DISTINCT ON (organization_id,subject) * FROM agent_access ORDER BY organization_id,subject,id DESC;
-- Access in force: the latest decision (approve or revoke) is an approval bound to the current membership.
-- A pending or declined request never changes access already approved.
CREATE VIEW effective_agent_access AS SELECT d.* FROM (SELECT DISTINCT ON (organization_id,subject) * FROM agent_access WHERE state IN ('approved','revoked') ORDER BY organization_id,subject,id DESC) d
 JOIN current_organization_members m ON m.organization_id=d.organization_id AND m.subject=d.subject AND m.granted AND m.id=d.member_access_id WHERE d.state='approved';
COMMIT;
