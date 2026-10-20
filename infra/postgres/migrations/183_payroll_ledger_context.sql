BEGIN;
ALTER TABLE preparation_jobs DROP CONSTRAINT preparation_jobs_state_check;
ALTER TABLE preparation_jobs ADD CONSTRAINT preparation_jobs_state_check CHECK(state IN ('waiting_evidence','interpreting','needs_confirmation','waiting_client','queued','completed','failed','cancelled','supporting'));

-- A source-only ledger's current reading and its payroll summary agree on its role. No accounting is approved here.
CREATE VIEW payroll_ledger_readings WITH (security_invoker=true) AS
SELECT DISTINCT parent.close_id,own.job_id AS ledger_job_id,ledger.id AS ledger_source_id,ledger.digest AS ledger_digest,
 p.job_id AS payroll_job_id,summary.id AS summary_source_id,summary.digest AS summary_digest
FROM preparation_intakes p JOIN preparation_jobs parent ON parent.id=p.job_id
JOIN evidence summary ON summary.id=p.source_id AND summary.close_id=parent.close_id AND summary.superseded_by IS NULL AND summary.extraction_state='ready'
CROSS JOIN LATERAL jsonb_array_elements(COALESCE(NULLIF(p.proposal->'sources','null'::jsonb),'[]'::jsonb)) used_source
JOIN evidence ledger ON used_source->>'id'=ledger.id::text AND ledger.close_id=parent.close_id AND ledger.superseded_by IS NULL AND ledger.extraction_state='ready'
JOIN preparation_intakes own ON own.source_id=ledger.id
JOIN preparation_jobs own_job ON own_job.id=own.job_id AND own_job.close_id=parent.close_id
WHERE parent.state NOT IN ('failed','cancelled') AND p.proposal->>'kind'='payroll' AND p.proposal->>'engine' IN ('openai','context_machine')
 AND used_source->>'role'='ledger' AND used_source->>'digest'=ledger.digest
 AND own.proposal->>'kind'='unsupported' AND own.proposal->>'engine' IN ('openai','context_machine')
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(own.proposal->'sources','null'::jsonb),'[]'::jsonb)) s WHERE s->>'id'=ledger.id::text AND s->>'role'='ledger' AND s->>'digest'=ledger.digest)
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(p.proposal->'sources','null'::jsonb),'[]'::jsonb)) s WHERE s->>'id'=summary.id::text AND s->>'role'='support' AND s->>'digest'=summary.digest);
COMMIT;
