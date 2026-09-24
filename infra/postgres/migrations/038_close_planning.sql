BEGIN;
ALTER TABLE client_closes ADD COLUMN due_on date;
ALTER TABLE client_closes ADD COLUMN priority text NOT NULL DEFAULT 'normal' CHECK (priority IN ('high','normal','low'));
ALTER TABLE client_closes ADD COLUMN owner text NOT NULL DEFAULT '' CHECK (length(owner) <= 200);
COMMIT;
