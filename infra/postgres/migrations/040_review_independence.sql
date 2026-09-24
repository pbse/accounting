BEGIN;
ALTER TABLE organizations ADD COLUMN require_independent_review boolean NOT NULL DEFAULT false;
ALTER TABLE organizations ADD CONSTRAINT organizations_independent_review_hosted CHECK (id <> 'local-development' OR NOT require_independent_review);
CREATE TABLE organization_policy_changes (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 independent_review boolean NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 5 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE workpapers ADD COLUMN created_by text;
COMMIT;
