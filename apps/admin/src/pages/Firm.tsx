import { Link, useParams } from "react-router";
import { useResource, when } from "../api";
import type { FirmRow } from "./Firms";
import { Stat } from "./Overview";

type Member = { subject: string; granted: boolean; decided_by: string; reason: string; decided_at: string };
type Run = { id: number; workflow: string; state: string; started_by: string; created_at: string; finished_at: string | null; error: string };
type Health = { run_id: number; finished_at: string; report: Record<string, number> } | null;

const stateTone: Record<string, string> = { completed: "ok", failed: "danger", cancelled: "", waiting: "info", running: "info", queued: "warn" };

export function Firm() {
  const { id = "" } = useParams();
  const firm = useResource<{ firm: FirmRow; members: Member[]; runs: Run[]; health: Health }>(`/v1/admin/firms/${encodeURIComponent(id)}`);
  const f = firm.data?.firm;
  return (
    <>
      <p><Link to="/firms">← Firms</Link></p>
      {firm.error && <p role="alert">{firm.error}</p>}
      {f && firm.data && (
        <>
          <div className="page-title">
            <div>
              <div>
                <h1>{f.name}</h1>
                <p>{f.id} · created {when(f.created_at)}</p>
              </div>
            </div>
            <span className={`status ${f.enabled ? "ok" : "danger"}`}>{f.enabled ? "Active" : "Suspended"}</span>
          </div>
          <div className="stats">
            <Stat label="Members" value={f.members} />
            <Stat label="Clients" value={f.clients} />
            <Stat label="Closes" value={f.closes} />
            <Stat label="Active runs" value={f.active_runs} />
            <Stat label="Failed runs" value={f.failed_runs_7d} note="in 7 days" tone={f.failed_runs_7d ? "warn" : "ok"} />
            <Stat label="Last activity" value={when(f.last_activity)} />
          </div>
          <section>
            <h2>Latest health check</h2>
            {firm.data.health ? (
              <>
                <p>Checked {when(firm.data.health.finished_at)} (run {firm.data.health.run_id}).</p>
                <table>
                  <tbody>
                    {Object.entries(firm.data.health.report).map(([name, count]) => (
                      <tr key={name}><th scope="row">{name.replaceAll("_", " ")}</th><td>{count}</td></tr>
                    ))}
                  </tbody>
                </table>
              </>
            ) : (
              <p>No health check has run for this firm yet. The worker runs one daily for every firm.</p>
            )}
          </section>
          <section>
            <h2>Members</h2>
            {firm.data.members.length === 0 ? <p>No member has been enrolled.</p> : (
              <table>
                <thead><tr><th>Member</th><th>Access</th><th>Decided by</th><th>Reason</th><th>When</th></tr></thead>
                <tbody>
                  {firm.data.members.map((m) => (
                    <tr key={m.subject}>
                      <td>{m.subject}</td>
                      <td><span className={`status ${m.granted ? "ok" : "danger"}`}>{m.granted ? "Granted" : "Revoked"}</span></td>
                      <td>{m.decided_by}</td>
                      <td>{m.reason}</td>
                      <td>{when(m.decided_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
          <section>
            <h2>Recent workflow runs</h2>
            {firm.data.runs.length === 0 ? <p>No workflow has run for this firm.</p> : (
              <table>
                <thead><tr><th>Run</th><th>Workflow</th><th>State</th><th>Started by</th><th>Started</th><th>Finished</th></tr></thead>
                <tbody>
                  {firm.data.runs.map((r) => (
                    <tr key={r.id} title={r.error || undefined}>
                      <td>{r.id}</td>
                      <td>{r.workflow}</td>
                      <td><span className={`status ${stateTone[r.state] ?? ""}`}>{r.state}</span></td>
                      <td>{r.started_by}</td>
                      <td>{when(r.created_at)}</td>
                      <td>{when(r.finished_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </>
      )}
    </>
  );
}
