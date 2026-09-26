BEGIN;
-- A client's current answer to each numbered question of an approved request, kept across partial replies.
CREATE TABLE work_request_answers (
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 position integer NOT NULL CHECK (position BETWEEN 1 AND 50),
 state text NOT NULL CHECK (state IN ('answered','awaiting_document')),
 answer text NOT NULL DEFAULT '' CHECK (char_length(answer) <= 4000),
 PRIMARY KEY (request_id,position)
);
-- Each reply accepted through a request link; a retry repeats the latest one's receipt.
CREATE TABLE request_submissions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 request_id bigint NOT NULL REFERENCES work_requests(id) ON DELETE CASCADE,
 receipt text NOT NULL,
 source_id bigint REFERENCES evidence(id),
 complete boolean NOT NULL
);
CREATE INDEX request_submissions_request ON request_submissions(request_id,id);
ALTER TABLE work_request_answers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON work_request_answers USING (current_setting('app.organization',true)='*' OR request_id IN (SELECT id FROM work_requests));
ALTER TABLE request_submissions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON request_submissions USING (current_setting('app.organization',true)='*' OR request_id IN (SELECT id FROM work_requests));
COMMIT;
