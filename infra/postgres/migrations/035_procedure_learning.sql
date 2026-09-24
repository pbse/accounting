BEGIN;
ALTER TABLE evidence ADD COLUMN purpose text NOT NULL DEFAULT 'work' CHECK(purpose IN ('work','procedure'));
ALTER TABLE procedure_versions ADD COLUMN method text NOT NULL DEFAULT '';
ALTER TABLE procedure_versions ADD COLUMN draft_id bigint UNIQUE;

CREATE TABLE procedure_drafts (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL DEFAULT '',
 kind text NOT NULL CHECK(kind IN ('prepaid','accrual')),
 method text NOT NULL,
 instructions text NOT NULL CHECK(length(instructions) BETWEEN 1 AND 4000),
 expense_account text NOT NULL DEFAULT '',
 prepaid_account text NOT NULL DEFAULT '',
 source_evidence_id bigint REFERENCES evidence(id),
 source_digest text NOT NULL DEFAULT '',
 source_quote text NOT NULL DEFAULT '',
 source_draft_ids jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(source_draft_ids)='array'),
 revision integer NOT NULL DEFAULT 1,
 created_by text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 activated_version_id bigint REFERENCES procedure_versions(id)
);
ALTER TABLE procedure_versions ADD CONSTRAINT procedure_versions_draft_id_fkey FOREIGN KEY (draft_id) REFERENCES procedure_drafts(id);
CREATE INDEX procedure_drafts_scope ON procedure_drafts(organization_id,lower(client_entity),kind,id DESC);

CREATE TABLE procedure_example_approvals (
 draft_id bigint NOT NULL REFERENCES procedure_drafts(id),
 workpaper_id bigint NOT NULL REFERENCES workpapers(id),
 workpaper_digest text NOT NULL,
 review_id text NOT NULL,
 draft_revision integer NOT NULL,
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 1000),
 approved_by text NOT NULL,
 approved_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (draft_id,workpaper_id)
);
COMMIT;
