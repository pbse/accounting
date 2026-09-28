import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { post, useResource, when } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "../components/ActionForm";
import type { FirmRow } from "./Firms";
import { FirmActivity, FirmAudit, FirmPeople } from "./FirmTabs";
import { Stat } from "./Overview";
import { stateTone } from "./Runs";

type Run = { id: number; workflow: string; state: string; started_by: string; created_at: string; finished_at: string | null };
type Health = { run_id: number; finished_at: string; report: Record<string, number> } | null;
type Detail = { firm: FirmRow; last_suspension: { at: string; by: string } | null; runs: Run[]; health: Health };

const tabs = [["", "Overview"], ["people", "People"], ["activity", "Activity"], ["audit", "Staff audit"]] as const;

export function Firm() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const tab = params.get("tab") ?? "";
  const firm = useResource<Detail>(`/v1/admin/firms/${encodeURIComponent(id)}`);
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
          <nav className="tabs" aria-label="Firm">
            {tabs.map(([value, label]) => (
              <Link key={value} to={value ? `?tab=${value}` : "?"} aria-current={tab === value ? "page" : undefined}>{label}</Link>
            ))}
          </nav>
          {/* Keyed by firm, so a reveal never carries over to another firm. */}
          {tab === "" && <Overview key={id} detail={firm.data} reload={firm.reload} />}
          {tab === "people" && <FirmPeople key={id} firm={id} onChanged={firm.reload} />}
          {tab === "activity" && <FirmActivity key={id} firm={id} />}
          {tab === "audit" && <FirmAudit key={id} firm={id} />}
        </>
      )}
    </>
  );
}

function Overview({ detail, reload }: { detail: Detail; reload: () => void }) {
  const { firm: f, runs, health, last_suspension } = detail;
  return (
    <>
      <div className="stats">
        <Stat label="Members" value={f.members} />
        <Stat label="Clients" value={f.clients} />
        <Stat label="Closes" value={f.closes} />
        <Stat label="Active runs" value={f.active_runs} />
        <Stat label="Failed runs" value={f.failed_runs_7d} note="in 7 days" tone={f.failed_runs_7d ? "warn" : "ok"} />
        <Stat label="Last activity" value={when(f.last_activity)} />
      </div>
      {last_suspension && (
        <p className="note">
          {f.enabled ? "Last suspended" : "Suspended"} {when(last_suspension.at)} by {last_suspension.by}. The reason is in the <Link to="?tab=activity">activity</Link>.
        </p>
      )}
      <section>
        <h2>Latest health check</h2>
        {health ? (
          <>
            <p>Checked {when(health.finished_at)} (<Link to={`/runs/${health.run_id}`}>run {health.run_id}</Link>).</p>
            <table>
              <tbody>
                {Object.entries(health.report).map(([name, count]) => (
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
        <h2>Recent workflow runs</h2>
        {runs.length === 0 ? <p>No workflow has run for this firm.</p> : (
          <table>
            <thead><tr><th>Run</th><th>State</th><th>Started by</th><th>Started</th><th>Finished</th></tr></thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id}>
                  <td><Link to={`/runs/${r.id}`}>#{r.id} {r.workflow}</Link></td>
                  <td><span className={`status ${stateTone[r.state] ?? ""}`}>{r.state}</span></td>
                  <td>{r.started_by}</td>
                  <td>{when(r.created_at)}</td>
                  <td>{when(r.finished_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p>
          <Link to={`/runs?firm=${encodeURIComponent(f.id)}`}>All runs for this firm</Link> · <Link to={`/jobs?firm=${encodeURIComponent(f.id)}`}>Stuck work</Link> · <Link to={`/connections?firm=${encodeURIComponent(f.id)}`}>Connections</Link>
        </p>
      </section>
      <Suspension firm={f} onDone={reload} />
    </>
  );
}

// Suspending stops the firm's members and background work until staff or the firm resume it; the
// staff member types the firm's ID to confirm either way.
function Suspension({ firm, onDone }: { firm: FirmRow; onDone: () => void }) {
  const [confirm, setConfirm] = useState("");
  const operate = usePermissions().includes("platform:operate");
  const action = firm.enabled ? "suspend" : "resume";
  return (
    <section>
      <h2>{firm.enabled ? "Suspend this firm" : "Resume this firm"}</h2>
      <p>{firm.enabled ? "Members are refused and background work for the firm stops until it is resumed. Nothing is deleted." : "Members can use the app again and background work for the firm resumes."}</p>
      <ActionForm label={firm.enabled ? "Suspend firm" : "Resume firm"} danger={firm.enabled} allowed={operate} minLength={10} onSubmit={async (reason) => {
        if (confirm.trim() !== firm.id) throw new Error("Type the firm's ID exactly to confirm.");
        await post(`/v1/admin/firms/${encodeURIComponent(firm.id)}/${action}`, { confirm: confirm.trim(), reason });
        setConfirm("");
        onDone();
      }}>
        <label>Type the firm's ID ({firm.id}) to confirm<input value={confirm} autoComplete="off" onChange={(event) => setConfirm(event.target.value)} /></label>
      </ActionForm>
    </section>
  );
}
