-- A watch step (wf.Watch): one row for a function the engine calls every interval until it reports done.
ALTER TABLE workflow_steps DROP CONSTRAINT workflow_steps_kind_check;
ALTER TABLE workflow_steps ADD CONSTRAINT workflow_steps_kind_check CHECK (kind IN ('step','effect','activity','sleep','event','approval','child','value','watch'));
