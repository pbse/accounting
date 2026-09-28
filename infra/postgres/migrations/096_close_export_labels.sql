BEGIN;
-- A person's label on the uploaded exports a close without a live ledger connection uses: when they were produced,
-- whether they cover the whole month, and whether they already hold the final close adjustments. The latest row counts.
CREATE TABLE close_export_labels (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 close_id bigint NOT NULL REFERENCES client_closes(id) ON DELETE CASCADE,
 evidence_ids bigint[] NOT NULL CHECK (cardinality(evidence_ids) BETWEEN 1 AND 20),
 exported_on date NOT NULL,
 complete boolean NOT NULL,
 final_adjustments boolean NOT NULL,
 note text NOT NULL CHECK (length(note) BETWEEN 5 AND 500),
 actor text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX close_export_labels_latest ON close_export_labels(close_id,id DESC);
ALTER TABLE close_export_labels ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON close_export_labels USING (current_setting('app.organization',true)='*' OR close_id IN (SELECT id FROM client_closes));
COMMIT;
