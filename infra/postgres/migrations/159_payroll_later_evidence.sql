BEGIN;
-- An amended payroll summary arriving after a payroll draft reopens it, as a relevant credit or amended invoice reopens
-- a prepaid draft (155): a document read after the draft was prepared, which names the draft's payroll provider (whole
-- words) and speaks of an amendment, correction, revision, reissue, void or adjustment, is linked to that draft, whose
-- review state is then source_changed. As for prepaids, at worst a person looks at a document once more. Documents
-- already readable are not linked here: only those that become readable from now on.
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
END $$;
COMMIT;
