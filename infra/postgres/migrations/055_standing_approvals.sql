BEGIN;
-- A reviewer's approval of a prepaid schedule for its later carried months, while nothing changes.
CREATE TABLE standing_approvals (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL,
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 review_id bigint NOT NULL REFERENCES workpaper_reviews(id) ON DELETE CASCADE,
 granted_by text NOT NULL,
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 2000),
 created_at timestamptz NOT NULL DEFAULT now(),
 revoked_by text,
 revoked_at timestamptz
);
CREATE UNIQUE INDEX standing_approvals_active ON standing_approvals(workpaper_id) WHERE revoked_at IS NULL;
ALTER TABLE standing_approvals ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON standing_approvals USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
