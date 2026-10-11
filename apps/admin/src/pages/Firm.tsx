import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { post, useResource, when, whenParts } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "../components/ActionForm";
import type { FirmRow } from "./Firms";
import { FirmActivity, FirmAudit, FirmPeople } from "./FirmTabs";
import { stateTone } from "./Runs";
import { BackLink } from "@close/ui/components/BackLink";
import { PageHeader } from "@close/ui/components/PageHeader";
import { Segmented } from "@close/ui/components/Segmented";
import { StatGroup } from "@close/ui/components/StatTile";
import { Stat } from "../components/Stat";

type Run = { id: number; workflow: string; state: string; started_by: string; created_at: string; finished_at: string | null };
type Health = { run_id: number; finished_at: string; report: Record<string, number> } | null;
type Limits = { max_closes: number; max_pending_jobs: number; max_model_calls_per_day: number };
type Detail = { firm: FirmRow; last_suspension: { at: string; by: string } | null; runs: Run[]; health: Health; limits: Limits; support_owner: string; assistance_minutes: number; file_imports?: boolean };

const tabs = [["", "Overview"], ["people", "People"], ["activity", "Activity"], ["audit", "Staff audit"]] as const;

export function Firm() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const tab = params.get("tab") ?? "";
  const firm = useResource<Detail>(`/v1/admin/firms/${encodeURIComponent(id)}`);
  const f = firm.data?.firm;
  return (
    <>
      <BackLink to="/firms">Firms</BackLink>
      {firm.error && <p role="alert">{firm.error}</p>}
      {f && firm.data && (
        <>
          <PageHeader
            title={f.name}
            actions={
              <span className={`status ${f.enabled ? "ok" : "danger"}`}>{f.enabled ? "Active" : "Suspended"}</span>
            }
          >
            {f.id} · created {when(f.created_at)}
          </PageHeader>
          <nav className="toolbar" aria-label="Firm">
            <Segmented items={tabs.map(([value, label]) => ({ key: value, label, to: value ? `?tab=${value}` : "?" }))} value={tab} />
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
      <StatGroup>
        <Stat label="Members" value={f.members} />
        <Stat label="Clients" value={f.clients} />
        <Stat label="Closes" value={f.closes} />
        <Stat label="Active runs" value={f.active_runs} />
        <Stat label="Failed runs" value={f.failed_runs_7d} note="in 7 days" tone={f.failed_runs_7d ? "warn" : "ok"} />
        <Stat label="Last activity" value={whenParts(f.last_activity).day} note={whenParts(f.last_activity).time} />
      </StatGroup>
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
      <Onboarding key={detail.support_owner} firm={f} detail={detail} onDone={reload} />
      <FirmLimits key={JSON.stringify(detail.limits)} firm={f} limits={detail.limits} onDone={reload} />
      <FileImports firm={f} on={!!detail.file_imports} onDone={reload} />
      <Suspension firm={f} onDone={reload} />
    </>
  );
}

// The firm's named support owner, and our team's setup and service time on the firm or one of its closes.
function Onboarding({ firm, detail, onDone }: { firm: FirmRow; detail: Detail; onDone: () => void }) {
  const [owner, setOwner] = useState(detail.support_owner);
  const [minutes, setMinutes] = useState(30);
  const [closeId, setCloseId] = useState("");
  const operate = usePermissions().includes("platform:operate");
  const base = `/v1/admin/firms/${encodeURIComponent(firm.id)}`;
  return (
    <section>
      <h2>Support</h2>
      <p>{detail.support_owner ? `Support owner: ${detail.support_owner}.` : "No support owner is named."} Our team's recorded time: {detail.assistance_minutes} minutes.</p>
      <ActionForm label="Name the support owner" allowed={operate} minLength={10} onSubmit={async (reason) => {
        await post(`${base}/support-owner`, { owner: owner.trim(), reason });
        onDone();
      }}>
        <label>Support owner<input value={owner} maxLength={200} onChange={(event) => setOwner(event.target.value)} /></label>
      </ActionForm>
      <ActionForm label="Record our time" allowed={operate} minLength={10} onSubmit={async (note) => {
        await post(`${base}/assistance`, { close_id: closeId.trim(), minutes, note });
        setCloseId("");
        onDone();
      }}>
        <label>Minutes<input type="number" min={1} max={480} required value={minutes} onChange={(event) => setMinutes(Number(event.target.value))} /></label>
        <label>Close ID (blank for the firm itself)<input value={closeId} inputMode="numeric" onChange={(event) => setCloseId(event.target.value)} /></label>
      </ActionForm>
    </section>
  );
}

// A firm's capacity, changed from the values shown so two staff members cannot overwrite each other.
function FirmLimits({ firm, limits, onDone }: { firm: FirmRow; limits: Limits; onDone: () => void }) {
  const [after, setAfter] = useState(limits);
  const operate = usePermissions().includes("platform:operate");
  const field = (key: keyof Limits, label: string, max: number) => (
    <label>{label}<input type="number" min={1} max={max} required value={after[key]} onChange={(event) => setAfter({ ...after, [key]: Number(event.target.value) })} /></label>
  );
  return (
    <section>
      <h2>Limits</h2>
      <p>Closes, active preparation jobs and model calls per UTC day. Check capacity and backlog before raising them.</p>
      <ActionForm label="Change limits" allowed={operate} minLength={10} onSubmit={async (reason) => {
        await post(`/v1/admin/firms/${encodeURIComponent(firm.id)}/limits`, { before: limits, after, reason });
        onDone();
      }}>
        {field("max_closes", "Closes", 10000)}
        {field("max_pending_jobs", "Active preparation jobs", 10000)}
        {field("max_model_calls_per_day", "Model calls per day", 100000)}
      </ActionForm>
    </section>
  );
}

// File imports take a client's books as files. They stay off for every firm until internal validation; the
// demonstration firm and internal firms come first (docs/books-sources.md, Delivery).
function FileImports({ firm, on, onDone }: { firm: FirmRow; on: boolean; onDone: () => void }) {
  const operate = usePermissions().includes("platform:operate");
  return (
    <section>
      <h2>File imports</h2>
      <p>{on ? "On: members can send clients' books as files." : "Off: clients' books arrive only from connected ledgers and labeled exports."}</p>
      <ActionForm label={on ? "Switch file imports off" : "Switch file imports on"} allowed={operate} minLength={10} onSubmit={async (reason) => {
        await post(`/v1/admin/firms/${encodeURIComponent(firm.id)}/file-imports`, { enabled: !on, reason });
        onDone();
      }} />
    </section>
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
