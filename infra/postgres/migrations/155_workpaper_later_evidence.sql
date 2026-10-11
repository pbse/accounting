BEGIN;
-- A document read after a prepaid draft was prepared, which names the draft's invoice (its vendor or invoice number, as
-- whole words) and speaks of a credit, cancellation, refund, adjustment, reversal, void or amendment, may change that
-- draft: a relevant credit or amended invoice invalidates the preparation it affects. The trigger below links such a
-- document to each earlier prepaid draft of its close as the document becomes readable, whatever brought it (an upload,
-- an email, a client's reply, a forwarded file), and the draft's review state is then source_changed: it cannot be
-- approved as it is and its review reopens. Preparing it again makes a new draft, later than the document, which the
-- link does not touch; a document replaced later stops holding the draft.
CREATE TABLE workpaper_later_evidence (
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 workpaper_id bigint NOT NULL REFERENCES workpapers(id) ON DELETE CASCADE,
 evidence_id bigint NOT NULL REFERENCES evidence(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (workpaper_id, evidence_id)
);
ALTER TABLE workpaper_later_evidence ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON workpaper_later_evidence USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));

-- link_later_evidence_for links a readable work document to each prepaid draft of its close prepared before it became
-- readable (readable_at), whose invoice (the vendor and invoice number its preparation job's reading cited) it names.
CREATE FUNCTION link_later_evidence_for(ev evidence, readable_at timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE text_lower text := lower(COALESCE(ev.content,''));
BEGIN
  IF replace(text_lower,'credit card','') !~ '(credit|cancel|refund|adjust|revers|void|amend)' THEN
    RETURN;
  END IF;
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
END $$;

-- A document becomes readable when it is added ready, or when its reading finishes or changes; a draft prepared after
-- that moment had it to weigh.
CREATE FUNCTION link_later_evidence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP='UPDATE' AND OLD.extraction_state='ready' AND OLD.content IS NOT DISTINCT FROM NEW.content AND OLD.superseded_by IS NOT DISTINCT FROM NEW.superseded_by THEN
    RETURN NULL;
  END IF;
  PERFORM link_later_evidence_for(NEW, now());
  RETURN NULL;
END $$;
CREATE TRIGGER link_later_evidence AFTER INSERT OR UPDATE OF extraction_state,content,superseded_by ON evidence FOR EACH ROW
 WHEN (NEW.purpose='work' AND NEW.extraction_state='ready' AND NEW.superseded_by IS NULL) EXECUTE FUNCTION link_later_evidence();

-- Documents already readable: when each became readable is not recorded, so every prepaid draft of its month that it
-- names is linked (at worst a person looks at a credit once more). In a signed-off month only documents that arrived
-- after the latest sign-off count: the person who signed it had the earlier ones before them.
SELECT link_later_evidence_for(e,'infinity') FROM evidence e WHERE e.purpose='work' AND e.extraction_state='ready' AND e.superseded_by IS NULL
 AND NOT EXISTS(SELECT 1 FROM (SELECT decision,created_at FROM close_signoffs WHERE close_id=e.close_id ORDER BY id DESC LIMIT 1) s WHERE s.decision='signed_off' AND s.created_at>=e.created_at);

-- The draft's own review state (before dependencies): source_changed also while a linked later document is current.
CREATE OR REPLACE VIEW workpaper_source_review_state WITH (security_invoker=true) AS
 SELECT w.id,w.close_id,w.kind,w.digest,w.paper,w.created_at,COALESCE(r.id::text,'') AS review_id,
 CASE WHEN EXISTS (SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text) OR EXISTS (
   SELECT 1 FROM jsonb_array_elements(COALESCE(NULLIF(w.paper->'sources','null'::jsonb),'[]'::jsonb)) s
   LEFT JOIN evidence e ON e.id::text=s->>'id' AND e.close_id=w.close_id
   WHERE e.id IS NULL OR e.digest IS DISTINCT FROM s->>'digest' OR e.superseded_by IS NOT NULL
 ) OR EXISTS (
   SELECT 1 FROM workpaper_later_evidence l JOIN evidence e ON e.id=l.evidence_id WHERE l.workpaper_id=w.id AND e.superseded_by IS NULL
 ) THEN 'source_changed' ELSE COALESCE(r.decision,'pending') END AS review_state
 FROM workpapers w LEFT JOIN LATERAL (
   SELECT id,decision FROM workpaper_reviews WHERE workpaper_id=w.id ORDER BY id DESC LIMIT 1
 ) r ON true;
COMMIT;
