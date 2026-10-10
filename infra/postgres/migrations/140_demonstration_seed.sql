BEGIN;
-- The demonstration seed (docs/seed-data.md) records what it put into a firm: who ran it, the policy it found, and
-- the rows it added that no client name identifies. Removal takes out exactly these and restores the policy, and a
-- firm with a record is not seeded again until it is removed.
CREATE TABLE demonstration_seeds (
 organization_id text PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
 subject text NOT NULL,
 members text[] NOT NULL DEFAULT '{}',
 -- created_firm says the seed created the firm row and its memberships (a local database), so removal may take them.
 created_firm boolean NOT NULL DEFAULT false,
 policy jsonb NOT NULL,
 close_ids bigint[] NOT NULL DEFAULT '{}',
 chat_session_ids bigint[] NOT NULL DEFAULT '{}',
 invitation_ids bigint[] NOT NULL DEFAULT '{}',
 role_change_ids bigint[] NOT NULL DEFAULT '{}',
 agent_access_ids bigint[] NOT NULL DEFAULT '{}',
 procedure_ids bigint[] NOT NULL DEFAULT '{}',
 attention_keys text[] NOT NULL DEFAULT '{}',
 model_call_days date[] NOT NULL DEFAULT '{}',
 started_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz
);
ALTER TABLE demonstration_seeds ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON demonstration_seeds USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
