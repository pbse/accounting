BEGIN;
-- One row per written-answer attempt: its outcome and how long it took. No question or answer text is kept.
CREATE TABLE assistant_answers (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 author text NOT NULL,
 close_id bigint REFERENCES client_closes(id) ON DELETE CASCADE,
 outcome text NOT NULL CHECK (outcome IN ('kept','not_kept','unanswerable','failed','limit')),
 latency_ms integer,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX assistant_answers_window ON assistant_answers(organization_id,created_at);
ALTER TABLE assistant_answers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON assistant_answers USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
