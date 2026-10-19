BEGIN;
-- Later readable lender changes reopen saved loan schedules through the existing source-review link.
-- Match the complete lender conservatively: multiple loans from that lender require a person to place the notice.
CREATE OR REPLACE FUNCTION link_later_loan_evidence_for(ev evidence, readable_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE text_lower text := lower(COALESCE(ev.content,''));
BEGIN
    INSERT INTO workpaper_later_evidence(close_id,workpaper_id,evidence_id)
    SELECT ev.close_id,w.id,ev.id FROM workpapers w
    WHERE w.close_id=ev.close_id AND w.kind='loan' AND w.created_at<readable_at
     AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(w.paper->'sources','null'::jsonb),'[]'::jsonb)) s WHERE s->>'id'=ev.id::text)
     AND COALESCE(lower(btrim(w.paper->'input'->>'lender')),'')<>''
     AND text_lower ~ ('(^|[^0-9a-z])'||regexp_replace(lower(btrim(w.paper->'input'->>'lender')),'([.^$*+?()\[\]{}|\\-])','\\\1','g')||'([^0-9a-z]|$)')
    ON CONFLICT DO NOTHING;
END $$;
CREATE OR REPLACE FUNCTION link_later_evidence_for(ev evidence, readable_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE text_lower text := lower(COALESCE(ev.content,''));
BEGIN
  IF replace(text_lower,'credit card','') ~ '(credit|cancel|refund|adjust|revers|void|amend)' THEN
    INSERT INTO workpaper_later_evidence(close_id,workpaper_id,evidence_id)
    SELECT ev.close_id,w.id,ev.id FROM workpapers w
    CROSS JOIN LATERAL (
      SELECT max(CASE WHEN f->>'field'='vendor' THEN lower(btrim(f->>'value')) END) AS vendor,
             max(CASE WHEN f->>'field'='invoice_number' THEN lower(btrim(f->>'value')) END) AS number
      FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id
      CROSS JOIN jsonb_array_elements(COALESCE(NULLIF(i.proposal->'facts','null'::jsonb),'[]'::jsonb)) f
      WHERE j.close_id=w.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text)
    ) x
    WHERE w.close_id=ev.close_id AND w.kind='prepaid' AND w.created_at<readable_at
     AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(w.paper->'sources','null'::jsonb),'[]'::jsonb)) s WHERE s->>'id'=ev.id::text)
     AND (COALESCE(x.vendor,'')<>'' AND text_lower ~ ('(^|[^0-9a-z])'||regexp_replace(x.vendor,'([.^$*+?()\[\]{}|\\-])','\\\1','g')||'([^0-9a-z]|$)')
       OR COALESCE(x.number,'')<>'' AND text_lower ~ ('(^|[^0-9a-z])'||regexp_replace(x.number,'([.^$*+?()\[\]{}|\\-])','\\\1','g')||'([^0-9a-z]|$)'))
    ON CONFLICT DO NOTHING;
  END IF;
  IF text_lower ~ '(amend|correct|revis|reissu|void|adjust)' THEN
    INSERT INTO workpaper_later_evidence(close_id,workpaper_id,evidence_id)
    SELECT ev.close_id,w.id,ev.id FROM workpapers w
    WHERE w.close_id=ev.close_id AND w.kind='payroll' AND w.created_at<readable_at
     AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(w.paper->'sources','null'::jsonb),'[]'::jsonb)) s WHERE s->>'id'=ev.id::text)
     AND COALESCE(lower(btrim(w.paper->'input'->>'provider')),'')<>''
     AND text_lower ~ ('(^|[^0-9a-z])'||regexp_replace(lower(btrim(w.paper->'input'->>'provider')),'([.^$*+?()\[\]{}|\\-])','\\\1','g')||'([^0-9a-z]|$)')
    ON CONFLICT DO NOTHING;
  END IF;
  PERFORM link_later_loan_evidence_for(ev,readable_at);
END $$;
-- Existing notices added after their loan draft also need review; an already signed-off close retains its scope.
SELECT link_later_loan_evidence_for(e,e.created_at) FROM evidence e
 WHERE e.purpose='work' AND e.extraction_state='ready' AND e.superseded_by IS NULL
 AND EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id=e.close_id AND w.kind='loan' AND w.created_at<e.created_at)
 AND NOT EXISTS(SELECT 1 FROM (SELECT decision,created_at FROM close_signoffs WHERE close_id=e.close_id ORDER BY id DESC LIMIT 1) s WHERE s.decision='signed_off' AND s.created_at>=e.created_at);
COMMIT;
