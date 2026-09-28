import { useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { post, useResource } from "../api";
import { SchemaForm } from "../components/SchemaForm";
import { usePermissions } from "../App";

type Definition = { name: string; title: string; input_schema: Record<string, unknown>; triggers: { Kind: string; Value: string }[] };

// Staff start only workflows with a manual trigger; one whose manual triggers all say "firm" needs a firm.
const manual = (d: Definition) => d.triggers.filter((t) => t.Kind === "manual");

export function StartRun() {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const definitions = useResource<{ workflows: Definition[] }>("/v1/admin/workflows/definitions");
  const [workflow, setWorkflow] = useState(params.get("workflow") ?? "");
  const [firm, setFirm] = useState(params.get("firm") ?? "");
  const [input, setInput] = useState<{ value: unknown; valid: boolean }>({ value: {}, valid: true });
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  // One key per visit to this form: pressing Start twice starts one run.
  const idempotencyKey = useMemo(() => crypto.randomUUID(), []);
  const chosen = definitions.data?.workflows.find((w) => w.name === workflow);
  const allowed = usePermissions().includes("platform:operate");
  const needsFirm = !!chosen && manual(chosen).every((t) => t.Value === "firm");
  return (
    <>
      <div className="page-title"><div><div><h1>Start a run</h1><p>Runs as the firm you name, recorded with your reason.</p></div></div></div>
      {definitions.error && <p role="alert">{definitions.error}</p>}
      <section>
        <form className="stack" onSubmit={async (event) => {
          event.preventDefault();
          setBusy(true);
          setError("");
          try {
            const result = await post<{ id: number }>("/v1/admin/workflows/runs", { workflow, organization: firm.trim(), input: input.value, idempotency_key: idempotencyKey, reason });
            navigate(`/runs/${result.id}`);
          } catch (e) {
            setError((e as Error).message);
          } finally {
            setBusy(false);
          }
        }}>
          <label>Workflow
            <select required value={workflow} onChange={(event) => { setWorkflow(event.target.value); setInput({ value: {}, valid: true }); }}>
              <option value="">Choose…</option>
              {definitions.data?.workflows.filter((w) => manual(w).length > 0).map((w) => <option key={w.name} value={w.name}>{w.title} ({w.name})</option>)}
            </select>
          </label>
          <label>Firm ID
            <input value={firm} required={needsFirm} placeholder={needsFirm ? "org_…" : "org_… (leave empty for platform work)"} onChange={(event) => setFirm(event.target.value)} />
          </label>
          {chosen && <SchemaForm key={chosen.name} schema={chosen.input_schema as never} onChange={(value, valid) => setInput({ value, valid })} />}
          <label>Why are you starting it?
            <input required value={reason} onChange={(event) => setReason(event.target.value)} maxLength={500} />
          </label>
          {error && <p role="alert">{error}</p>}
          {!allowed && <p className="muted">Starting runs needs platform:operate, which your role does not have.</p>}
          <button className="primary" disabled={!allowed || busy || !workflow || !input.valid || !reason.trim() || (needsFirm && !firm.trim())}>Start run</button>
        </form>
      </section>
    </>
  );
}
