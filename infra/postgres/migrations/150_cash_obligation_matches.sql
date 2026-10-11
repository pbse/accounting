BEGIN;
-- How an open bill relates to one date of an approved cash schedule (docs/cash-plan.md, "Bills and schedules"): a
-- person's decision, never inferred from a vendor and a week. The bill replaces that date's payment, invoices part of
-- it (the rest still counts), is a separate payment, or repeats a payment already counted. A later decision for the
-- same bill and date retires the earlier one; rows are otherwise only added, so every past decision stays readable.
CREATE TABLE cash_obligation_matches (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 obligation_id bigint NOT NULL REFERENCES cash_obligations(id),
 occurrence_on date NOT NULL,
 -- company is the ledger the bill is in ('quickbooks:production:123'); document names the bill there ('bill:9').
 company text NOT NULL CHECK (char_length(company) BETWEEN 1 AND 300),
 document text NOT NULL CHECK (document ~ '^bill:[A-Za-z0-9_.-]{1,64}$'),
 label text NOT NULL CHECK (char_length(label) BETWEEN 1 AND 300),
 -- document_date is the bill's own date, so a capture is known to cover it before its absence says anything.
 document_date date,
 relation text NOT NULL CHECK (relation IN ('replaces','part','separate','duplicate')),
 -- amount is what the bill invoices of the schedule, stated when decided; a part's residual is the schedule's amount
 -- less these, so a later credit or partial payment on the bill does not change what is still to be invoiced.
 amount numeric(18,2) NOT NULL CHECK (amount > 0),
 -- paid is a person's confirmation that the bill, no longer open, was paid: a capture shows only that it left the open
 -- items (paid, voided or credited), so until then the schedule's estimate counts for it.
 paid boolean NOT NULL DEFAULT false,
 decided_by text NOT NULL,
 decided_at timestamptz NOT NULL DEFAULT now(),
 retired_by text,
 retired_at timestamptz,
 CHECK ((retired_by IS NULL) = (retired_at IS NULL))
);
CREATE INDEX cash_obligation_matches_client ON cash_obligation_matches(organization_id, lower(client_entity), company) WHERE retired_at IS NULL;
-- One standing decision per bill and date, and a bill pays (in full or in part) or repeats at most one date.
CREATE UNIQUE INDEX cash_obligation_matches_pair ON cash_obligation_matches(organization_id, company, document, obligation_id, occurrence_on) WHERE retired_at IS NULL;
CREATE UNIQUE INDEX cash_obligation_matches_paid_once ON cash_obligation_matches(organization_id, company, document) WHERE retired_at IS NULL AND relation IN ('replaces','part','duplicate');
ALTER TABLE cash_obligation_matches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON cash_obligation_matches USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
