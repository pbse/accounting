import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { usePermissions } from "../App";
import { get, post, when } from "../api";
import { FlowGraph, type PlanNode, type Step } from "../components/Flow";
import { stateTone, type RunRow } from "./Runs";

type Detail = {
  run: RunRow;
  input: unknown;
  output: unknown;
  steps: Step[];
  children: { id: number; workflow: string; state: string; parent_step: string }[];
  plan: { id: number; title: string; state: string; source: string; plan: { nodes: PlanNode[] } } | null;
  registered: boolean;
  revealed: boolean;
};

const active = ["queued", "running", "waiting"];

// An action staff take on a run: it always asks why, and the answer goes to the audit log.
function ActionForm({ label, danger, allowed = true, onSubmit, children }: { label: string; danger?: boolean; allowed?: boolean; onSubmit: (reason: string) => Promise<void>; children?: ReactNode }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!allowed) return <p className="muted">{label}: your role does not allow this.</p>;
  return (
    <form className="action" onSubmit={async (event) => {
      event.preventDefault();
      setBusy(true);
      setError("");
      try {
        await onSubmit(reason.trim());
        setReason("");
      } catch (e) {
        setError((e as Error).message);
      } finally {
        setBusy(false);
      }
    }}>
      {children}
      <label>Reason<input required value={reason} maxLength={500} onChange={(event) => setReason(event.target.value)} /></label>
      <button className={danger ? "danger" : "primary"} disabled={busy || !reason.trim()}>{label}</button>
      {error && <p role="alert">{error}</p>}
    </form>
  );
}

function Json({ value }: { value: unknown }) {
  if (value === null || value === undefined) return <p className="muted">None</p>;
  if (typeof value === "object" && (value as { hidden?: boolean }).hidden) {
    return <p className="muted">Hidden ({(value as { bytes: number }).bytes} bytes). Reveal client content to see it.</p>;
  }
  return <pre className="json">{JSON.stringify(value, null, 2)}</pre>;
}

export function Run() {
  const { id = "" } = useParams();
  const [detail, setDetail] = useState<Detail>();
  const [error, setError] = useState("");
  const [reveal, setReveal] = useState<string>();
  const [params, setParams] = useSearchParams();
  const selected = params.get("step") ?? undefined;
  const setSelected = (key: string) => setParams({ step: key }, { replace: true });
  const can = usePermissions();
  const operate = can.includes("platform:operate");
  useEffect(() => {
    setDetail(undefined);
    setReveal(undefined);
  }, [id]);
  const load = useCallback(async () => {
    try {
      const path = `/v1/admin/workflows/runs/${encodeURIComponent(id)}${reveal ? "?reveal=1" : ""}`;
      setDetail(await get<Detail>(path, reveal ? { "X-Access-Reason": reveal } : {}));
      setError("");
    } catch (e) {
      setError((e as Error).message);
      setDetail(undefined);
      setReveal(undefined);
    }
  }, [id, reveal]);
  useEffect(() => {
    load();
  }, [load]);
  // Follow an active run every 3 seconds; a reveal is audited, so it is read once, not polled.
  const running = detail && active.includes(detail.run.state);
  useEffect(() => {
    if (!running || reveal) return;
    const timer = setInterval(load, 3000);
    return () => clearInterval(timer);
  }, [running, reveal, load]);

  if (error) return <><p><Link to="/runs">← Runs</Link></p><p role="alert">{error}</p></>;
  if (!detail) return <p role="status">Loading run…</p>;
  const { run } = detail;
  const step = detail.steps.find((s) => s.key === selected);
  const act = (path: string, body: object) => post(`/v1/admin/workflows/runs/${run.id}/${path}`, body).then(load);
  return (
    <>
      <p><Link to="/runs">← Runs</Link></p>
      <div className="page-title">
        <div>
          <div>
            <h1>#{run.id} {run.workflow}</h1>
            <p>
              {run.organization ? <Link to={`/firms/${encodeURIComponent(run.organization)}`}>{run.firm_name ?? run.organization}</Link> : "Platform work"}
              {" · started by "}{run.started_by} {when(run.created_at)}
              {run.finished_at && <> · finished {when(run.finished_at)}</>}
              {run.parent_run_id && <> · child of <Link to={`/runs/${run.parent_run_id}`}>#{run.parent_run_id}</Link></>}
            </p>
          </div>
        </div>
        <span className={`status ${stateTone[run.state] ?? ""}`}>{run.state}{run.waiting_on && ` · ${run.waiting_on}`}</span>
      </div>
      {!detail.registered && <p className="note">This build does not register the workflow {run.workflow}; a build that does will pick the run up.</p>}
      {run.error && <p role="alert">{run.error}</p>}
      {detail.plan && (
        <p className="note">Plan #{detail.plan.id} “{detail.plan.title}”, {detail.plan.source}-built, {detail.plan.state}.</p>
      )}
      <div className="run-layout">
        <section>
          <h2>Flow</h2>
          <FlowGraph steps={detail.steps} plan={detail.plan?.plan.nodes} selected={selected} onSelect={setSelected} />
          <h3>Steps in order</h3>
          <ol className="steps">
            {detail.steps.map((s) => (
              <li key={s.key}>
                <button className={s.key === selected ? "link selected" : "link"} onClick={() => setSelected(s.key)}>{s.key}</button>{" "}
                <small>{s.kind}</small> <span className={`status ${stateTone[s.state] ?? ""}`}>{s.state}</span>
              </li>
            ))}
          </ol>
        </section>
        <aside className="inspector">
          {step ? (
            <>
              <h2>{step.key}</h2>
              <p>{step.kind}{step.activity && ` · ${step.activity}`} · <span className={`status ${stateTone[step.state] ?? ""}`}>{step.state}</span></p>
              <dl>
                <dt>Attempts</dt><dd>{step.attempts}</dd>
                <dt>Started</dt><dd>{when(step.started_at)}</dd>
                <dt>Finished</dt><dd>{when(step.finished_at)}</dd>
                {step.waiting_on && <><dt>Waiting for</dt><dd>{step.waiting_on}</dd></>}
                {step.retry_at && <><dt>Retries at</dt><dd>{when(step.retry_at)}</dd></>}
                {step.wake_at && <><dt>Until</dt><dd>{when(step.wake_at)}</dd></>}
                {step.error && <><dt>Error</dt><dd>{step.error}</dd></>}
                {step.actor && <><dt>Decided by</dt><dd>{step.actor}{step.note && ` — ${step.note}`}</dd></>}
                {step.child_run_id && <><dt>Child run</dt><dd><Link to={`/runs/${step.child_run_id}`}>#{step.child_run_id}</Link></dd></>}
              </dl>
              <h3>Input</h3><Json value={step.input} />
              <h3>Output</h3><Json value={step.output} />
              {step.kind === "approval" && step.state === "waiting" && <p className="note">Waiting for a firm member to decide. Staff do not make firm decisions.</p>}
              {(step.state === "failed" || (step.state === "waiting" && step.retry_at)) && ["failed", "waiting"].includes(run.state) && (
                <ActionForm label={step.kind === "child" ? "Start a fresh child" : "Retry this step now"} allowed={operate} onSubmit={(reason) => act("retry", { key: step.key, reason })} />
              )}
              {step.kind === "event" && step.state === "waiting" && step.waiting_on && run.organization && (
                <SendEventForm allowed={operate} onSubmit={(payload, reason) => post("/v1/admin/workflows/events", { organization: run.organization, key: step.waiting_on, payload, reason }).then(load)} />
              )}
              {step.kind === "effect" && step.state === "unknown" && <ResolveForm allowed={operate} onSubmit={(applied, output, reason) => act("resolve", { key: step.key, applied, output, reason })} />}
            </>
          ) : (
            <p>Select a step in the flow or the list to see its details.</p>
          )}
        </aside>
      </div>
      {active.includes(run.state) && (
        <section>
          <h2>Cancel this run</h2>
          <p>A waiting run ends now; a running one at its next step. Provider calls in flight are never cut short.</p>
          <ActionForm label="Cancel run" danger allowed={operate} onSubmit={(reason) => act("cancel", { reason })} />
        </section>
      )}
      <section>
        <h2>Input and output</h2>
        {detail.revealed ? (
          <p className="note">Showing client content. This view was recorded in the audit log. <button onClick={() => setReveal(undefined)}>Hide it</button></p>
        ) : (
          <ActionForm label="Reveal client content" allowed={can.includes("platform:content")} onSubmit={async (reason) => setReveal(reason)} />
        )}
        <h3>Input</h3><Json value={detail.input} />
        <h3>Output</h3><Json value={detail.output} />
      </section>
      {detail.children.length > 0 && (
        <section>
          <h2>Child runs</h2>
          <ul>{detail.children.map((c) => <li key={c.id}><Link to={`/runs/${c.id}`}>#{c.id} {c.workflow}</Link> from step {c.parent_step} · {c.state}</li>)}</ul>
        </section>
      )}
    </>
  );
}

// Parses what staff typed as JSON; undefined when it is not JSON.
function parse(text: string): { value: unknown } | undefined {
  try {
    return { value: JSON.parse(text || "null") };
  } catch {
    return undefined;
  }
}

const notJSON = () => Promise.reject(new Error("That is not valid JSON."));

function SendEventForm({ allowed, onSubmit }: { allowed: boolean; onSubmit: (payload: unknown, reason: string) => Promise<void> }) {
  const [payload, setPayload] = useState("{}");
  return (
    <>
      <h3>Send the event</h3>
      <p>For when the signal arrived outside the app, such as a reply by phone.</p>
      <ActionForm label="Send event" allowed={allowed} onSubmit={(reason) => { const p = parse(payload); return p ? onSubmit(p.value, reason) : notJSON(); }}>
        <label>Payload (JSON)<textarea rows={3} value={payload} onChange={(event) => setPayload(event.target.value)} /></label>
      </ActionForm>
    </>
  );
}

function ResolveForm({ allowed, onSubmit }: { allowed: boolean; onSubmit: (applied: boolean, output: unknown, reason: string) => Promise<void> }) {
  const [applied, setApplied] = useState(true);
  const [output, setOutput] = useState("null");
  return (
    <>
      <h3>Resolve the uncertain call</h3>
      <p>Check the provider first. Applied completes the step without calling again; not applied lets it run again.</p>
      <ActionForm label="Record the outcome" allowed={allowed} onSubmit={(reason) => { const p = applied ? parse(output) : { value: null }; return p ? onSubmit(applied, p.value, reason) : notJSON(); }}>
        <fieldset>
          <legend>What did the provider do?</legend>
          <label className="check"><input type="radio" checked={applied} onChange={() => setApplied(true)} /> It acted (applied)</label>
          <label className="check"><input type="radio" checked={!applied} onChange={() => setApplied(false)} /> Nothing happened (not applied)</label>
        </fieldset>
        {applied && <label>The step's result (JSON)<textarea rows={3} value={output} onChange={(event) => setOutput(event.target.value)} /></label>}
      </ActionForm>
    </>
  );
}
