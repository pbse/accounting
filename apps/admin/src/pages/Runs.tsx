import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useResource, when } from "../api";
import { PageHeader } from "@close/ui/components/PageHeader";

export type RunRow = {
  id: number;
  organization: string | null;
  firm_name: string | null;
  workflow: string;
  state: string;
  waiting_on: string;
  started_by: string;
  error: string;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
  parent_run_id: number | null;
  plan_id: number | null;
};

export const stateTone: Record<string, string> = { completed: "ok", failed: "danger", cancelled: "", waiting: "info", running: "info", queued: "warn" };

const views: [string, string][] = [
  ["", "All runs"],
  ["active", "Active (queued, running or waiting)"],
  ["stuck", "Stuck (runnable but not picked up)"],
  ["waiting_on_client", "Waiting on a client for over 7 days"],
  ["unknown_effects", "Provider writes to reconcile"],
  ["failed_today", "Failed in 24 hours"],
];

export function Runs() {
  const [params, setParams] = useSearchParams();
  const [firm, setFirm] = useState(params.get("firm") ?? "");
  useEffect(() => setFirm(params.get("firm") ?? ""), [params]);
  const set = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    next.delete("offset");
    setParams(next);
  };
  const offset = Number(params.get("offset") ?? 0) || 0;
  const runs = useResource<{ runs: RunRow[]; has_more: boolean; next_offset: number }>(`/v1/admin/workflows/runs?${params}`);
  return (
    <>
      <PageHeader
        icon="run"
        title="Runs"
        actions={
          <div className="title-actions">
            <button onClick={runs.reload} disabled={runs.loading}>Refresh</button>
            <Link className="button primary" to="/runs/new">Start a run</Link>
          </div>
        }
      >
        Workflow runs across every firm, newest first.
      </PageHeader>
      <form className="filters" onSubmit={(event) => { event.preventDefault(); set("firm", firm.trim()); }}>
        <label>View
          <select value={params.get("view") ?? ""} onChange={(event) => set("view", event.target.value)}>
            {views.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          </select>
        </label>
        <label>State
          <select value={params.get("state") ?? ""} onChange={(event) => set("state", event.target.value)}>
            {["", "queued", "running", "waiting", "completed", "failed", "cancelled"].map((s) => <option key={s} value={s}>{s || "Any"}</option>)}
          </select>
        </label>
        <label>Workflow
          <input value={params.get("workflow") ?? ""} placeholder="Any" onChange={(event) => set("workflow", event.target.value.trim())} />
        </label>
        <label>Firm ID
          <input value={firm} placeholder="Any" onChange={(event) => setFirm(event.target.value)} />
        </label>
        <button>Filter</button>
      </form>
      {params.get("parent") && <p className="note">Child runs of <Link to={`/runs/${params.get("parent")}`}>#{params.get("parent")}</Link>. <button className="link" onClick={() => set("parent", "")}>All runs</button></p>}
      {runs.error && <p role="alert">{runs.error}</p>}
      {runs.data && (
        <section>
          {runs.data.runs.length === 0 ? <p>No run matches.</p> : (
            <table>
              <thead><tr><th>Run</th><th>Firm</th><th>State</th><th>Started by</th><th>Started</th><th>Updated</th></tr></thead>
              <tbody>
                {runs.data.runs.map((r) => (
                  <tr key={r.id}>
                    <td><Link to={`/runs/${r.id}`}>#{r.id} {r.workflow}</Link>{r.parent_run_id && <small> child of #{r.parent_run_id}</small>}</td>
                    <td>{r.organization ? <Link to={`/firms/${encodeURIComponent(r.organization)}`}>{r.firm_name ?? r.organization}</Link> : "Platform"}</td>
                    <td><span className={`status ${stateTone[r.state] ?? ""}`}>{r.state}</span>{r.waiting_on && <small> {r.waiting_on}</small>}</td>
                    <td>{r.started_by}</td>
                    <td>{when(r.created_at)}</td>
                    <td>{when(r.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {(offset > 0 || runs.data.has_more) && (
            <p className="title-actions">
              {offset > 0 && <button onClick={() => set("offset", "")}>First page</button>}
              {runs.data.has_more && <button onClick={() => { const next = new URLSearchParams(params); next.set("offset", String(runs.data!.next_offset)); setParams(next); }}>More runs</button>}
            </p>
          )}
        </section>
      )}
    </>
  );
}
