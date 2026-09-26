BEGIN;
-- When each accepted link reply arrived, so the attention inbox can date it; earlier replies take the later of their reply
-- document's time and their request's newest attachment, which a documents-only reply added.
ALTER TABLE request_submissions ADD COLUMN created_at timestamptz;
UPDATE request_submissions s SET created_at=GREATEST(e.created_at,(SELECT max(a.created_at) FROM client_attachments a WHERE a.request_id=s.request_id)) FROM evidence e WHERE e.id=s.source_id;
ALTER TABLE request_submissions ALTER COLUMN created_at SET DEFAULT now();
-- When a job last changed state, so a new failure or confirmation is dated when it happened.
ALTER TABLE preparation_jobs ADD COLUMN state_changed_at timestamptz;
UPDATE preparation_jobs SET state_changed_at=COALESCE(finished_at,created_at);
ALTER TABLE preparation_jobs ALTER COLUMN state_changed_at SET DEFAULT now(), ALTER COLUMN state_changed_at SET NOT NULL;
CREATE FUNCTION preparation_job_state_changed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state IS DISTINCT FROM OLD.state THEN
  NEW.state_changed_at=now();
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER preparation_job_state_changed BEFORE UPDATE OF state ON preparation_jobs FOR EACH ROW EXECUTE FUNCTION preparation_job_state_changed();
-- A member's read and snooze marks on attention events, which are derived from work, review and request state.
CREATE TABLE attention_marks (
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL,
 key text NOT NULL CHECK (length(key) <= 200),
 read_at timestamptz,
 snoozed_until timestamptz,
 PRIMARY KEY (organization_id,subject,key)
);
-- Which kinds of attention events count toward a member's unread badge and browser notifications.
CREATE TABLE attention_preferences (
 organization_id text NOT NULL REFERENCES organizations(id),
 subject text NOT NULL,
 kinds text[] NOT NULL,
 browser boolean NOT NULL DEFAULT false,
 PRIMARY KEY (organization_id,subject)
);
ALTER TABLE attention_marks ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON attention_marks USING (current_setting('app.organization',true) IN ('*',organization_id));
ALTER TABLE attention_preferences ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON attention_preferences USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
