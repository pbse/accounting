BEGIN;
-- What a person decided to do about one open invoice or bill of a client's ledger company: who owns it, where it stands,
-- the date a customer promised to pay or the client's bill-pay tool will pay, and a note. Append-only: the newest row
-- per document counts. Whether the document is still open is always read from a capture, never set here.
CREATE TABLE open_item_actions (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 organization_id text NOT NULL REFERENCES organizations(id),
 client_entity text NOT NULL CHECK (length(client_entity) BETWEEN 1 AND 200),
 source text NOT NULL CHECK (source IN ('quickbooks','xero')),
 company text NOT NULL CHECK (length(company) BETWEEN 1 AND 300),
 kind text NOT NULL CHECK (kind IN ('Invoice','Bill')),
 item_id text NOT NULL CHECK (length(item_id) BETWEEN 1 AND 100),
 label text NOT NULL CHECK (length(label) BETWEEN 1 AND 300),
 document_date date,
 capture_id text NOT NULL,
 owner text NOT NULL DEFAULT '' CHECK (length(owner) <= 200),
 status text NOT NULL CHECK (status IN ('open','following_up','promised','disputed','handed_to_bill_pay','on_hold')),
 expected_on date,
 note text NOT NULL DEFAULT '' CHECK (length(note) <= 2000),
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 200),
 created_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((status IN ('promised','handed_to_bill_pay')) = (expected_on IS NOT NULL)),
 CHECK (status <> 'disputed' OR length(note) > 0),
 CHECK (status <> 'promised' OR kind = 'Invoice'),
 CHECK (status <> 'handed_to_bill_pay' OR kind = 'Bill')
);
CREATE INDEX open_item_actions_latest ON open_item_actions(organization_id, lower(client_entity), company, kind, item_id, id DESC);
ALTER TABLE open_item_actions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant ON open_item_actions USING (current_setting('app.organization',true) IN ('*',organization_id));
COMMIT;
