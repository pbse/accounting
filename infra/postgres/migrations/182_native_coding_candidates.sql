BEGIN;
ALTER TABLE bill_corrections DROP CONSTRAINT bill_corrections_state_check;
ALTER TABLE bill_corrections ADD CONSTRAINT bill_corrections_state_check CHECK(state IN ('candidate','dismissed','withdrawn','preview','approved','executing','unknown','verified'));
ALTER TABLE bill_corrections ADD COLUMN policy_version_id bigint REFERENCES procedure_versions(id), ADD COLUMN policy_evidence_id bigint, ADD COLUMN account_evidence_id bigint,
 ADD COLUMN candidate_reviews jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(candidate_reviews)='array');
ALTER TABLE bill_corrections ADD FOREIGN KEY(close_id,policy_evidence_id) REFERENCES evidence(close_id,id), ADD FOREIGN KEY(close_id,account_evidence_id) REFERENCES evidence(close_id,id),
 ADD CONSTRAINT native_candidate_sources CHECK(state<>'candidate' OR (policy_version_id IS NOT NULL AND policy_evidence_id IS NOT NULL AND account_evidence_id IS NOT NULL));
COMMIT;
