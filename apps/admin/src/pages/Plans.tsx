import { useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { post, when } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "../components/ActionForm";
import { FlowGraph, type PlanNode } from "../components/Flow";
import { Reveal, shown, useRevealable } from "../components/Reveal";

type PlanRow = {
  id: number;
  organization: string | null;
  firm_name: string | null;
  title: string;
  state: string;
  source: string;
  agent: string;
  rationale: string;
  created_by: string;
  approved_by: string | null;
  approved_at: string | null;
  is_template: boolean;
  template_id: number | null;
  run_id: number | null;
  digest: string;
  nodes: number;
  created_at: string;
  updated_at: string;
  plan?: { version: number; title: string; nodes: (PlanNode & { input?: unknown; approval?: { permission: string; subject?: string; prompt?: string; timeout?: string } })[] };
};

const tone: Record<string, string> = { draft: "", proposed: "warn", approved: "ok", rejected: "danger", archived: "" };
const firmOf = (p: PlanRow) => (p.organization ? <Link to={`/firms/${encodeURIComponent(p.organization)}`}>{p.firm_name ?? p.organization}</Link> : "Every firm");

// The queue and the rest: plans staff built and plans the agent proposed, newest first.
export function Plans() {
  const [params, setParams] = useSearchParams();
  const query = new URLSearchParams();
  for (const name of ["state", "source", "firm", "templates", "offset"]) if (params.get(name)) query.set(name, params.get(name)!);
  const offset = Number(params.get("offset") ?? 0) || 0;
  const plans = useRevealable<{ plans: PlanRow[]; has_more: boolean; next_offset: number; revealed: boolean }>(`/v1/admin/plans${query.size ? `?${query}` : ""}`);
  const set = (values: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [k, v] of Object.entries(values)) {
      if (v) next.set(k, v);
      else next.delete(k);
    }
    next.delete("offset");
    setParams(next);
  };
  const templates = params.get("templates") === "1";
  return (
    <>
      <div className="page-title"><div><div><h1>{templates ? "Plan templates" : "Plans"}</h1><p>{templates ? "Templates never run: copy one into a draft for a firm." : "Plans staff built and plans the agent proposed. Nothing runs until a person approves the exact version they reviewed."}</p></div></div></div>
      <form className="filters" onSubmit={(event) => {
        event.preventDefault();
        set(Object.fromEntries([...new FormData(event.currentTarget)].map(([k, v]) => [k, String(v).trim()])));
      }}>
        <label>Show
          <select name="templates" key={`t${templates}`} defaultValue={templates ? "1" : ""}>
            <option value="">Plans</option>
            <option value="1">Templates</option>
          </select>
        </label>
        <label>State
          <select name="state" key={`s${params.get("state")}`} defaultValue={params.get("state") ?? ""}>
            {["", "proposed", "draft", "approved", "rejected"].map((s) => <option key={s} value={s}>{s || "Any"}</option>)}
          </select>
        </label>
        <label>Source
          <select name="source" key={`o${params.get("source")}`} defaultValue={params.get("source") ?? ""}>
            {["", "staff", "agent", "member"].map((s) => <option key={s} value={s}>{s || "Any"}</option>)}
          </select>
        </label>
        <label>Firm ID<input name="firm" key={`f${params.get("firm")}`} defaultValue={params.get("firm") ?? ""} placeholder="Any" /></label>
        <button>Filter</button>
      </form>
      <Reveal reveal={plans.reveal} />
      {plans.error && <p role="alert">{plans.error}</p>}
      {!plans.data && !plans.error && <p role="status">Loading plans…</p>}
      {plans.data && (
        <section>
          {plans.data.plans.length === 0 ? <p>No plan matches.</p> : (
            <table>
              <thead><tr><th>Plan</th><th>Firm</th><th>State</th><th>Source</th><th>Steps</th><th>By</th><th>Updated</th></tr></thead>
              <tbody>
                {plans.data.plans.map((p) => (
                  <tr key={p.id}>
                    <td><Link to={`/plans/${p.id}`}>#{p.id}</Link> {shown(p.title)}</td>
                    <td>{firmOf(p)}</td>
                    <td><span className={`status ${tone[p.state] ?? ""}`}>{p.state}</span></td>
                    <td>{p.source}{p.agent && <small> {p.agent}</small>}</td>
                    <td>{p.nodes}</td>
                    <td>{p.created_by}</td>
                    <td>{when(p.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {(offset > 0 || plans.data.has_more) && (
            <p className="title-actions">
              {offset > 0 && <button onClick={() => set({})}>First page</button>}
              {plans.data.has_more && <button onClick={() => { const next = new URLSearchParams(params); next.set("offset", String(plans.data!.next_offset)); setParams(next); }}>More</button>}
            </p>
          )}
        </section>
      )}
    </>
  );
}

// One plan: drawn from its structure, with its content once revealed, and the decisions staff can take on it.
export function Plan() {
  const { id = "" } = useParams();
  return <PlanDetail key={id} id={id} />;
}

function PlanDetail({ id }: { id: string }) {
  const navigate = useNavigate();
  const can = usePermissions();
  const plan = useRevealable<{ plan: PlanRow; problems: string[]; approvable: boolean; revealed: boolean }>(`/v1/admin/plans/${encodeURIComponent(id)}`);
  const [firm, setFirm] = useState("");
  const base = `/v1/admin/plans/${encodeURIComponent(id)}`;
  if (plan.error) return <><p><Link to="/plans">← Plans</Link></p><p role="alert">{plan.error}</p></>;
  if (!plan.data) return <><p><Link to="/plans">← Plans</Link></p><p role="status">Loading the plan…</p></>;
  const { plan: p, problems, approvable, revealed } = plan.data;
  const nodes = p.plan?.nodes ?? [];
  return (
    <>
      <p><Link to={p.is_template ? "/plans?templates=1" : "/plans"}>← {p.is_template ? "Templates" : "Plans"}</Link></p>
      <div className="page-title">
        <div>
          <div>
            <h1>{p.is_template ? "Template" : "Plan"} #{p.id} {shown(p.title)}</h1>
            <p>
              {firmOf(p)} · {p.source}-built by {p.created_by}{p.agent && ` (${p.agent})`} · updated {when(p.updated_at)}
              {p.template_id && <> · from <Link to={`/plans/${p.template_id}`}>template #{p.template_id}</Link></>}
              {p.run_id && <> · <Link to={`/runs/${p.run_id}`}>run #{p.run_id}</Link></>}
            </p>
          </div>
        </div>
        <span className={`status ${tone[p.state] ?? ""}`}>{p.state}</span>
      </div>
      <Reveal reveal={plan.reveal} />
      {p.source === "agent" && <p className="note">The agent's reason: {shown(p.rationale)}</p>}
      {p.approved_by && <p className="note">Approved by {p.approved_by} {when(p.approved_at)}.</p>}
      <section>
        <h2>Flow</h2>
        <FlowGraph steps={[]} plan={nodes} onSelect={() => {}} />
        <table>
          <thead><tr><th>Step</th><th>Does</th><th>After</th><th>When</th>{revealed && <th>Input</th>}</tr></thead>
          <tbody>
            {nodes.map((n) => (
              <tr key={n.id}>
                <td>{n.id}</td>
                <td>
                  {n.activity && <>activity {n.activity}</>}
                  {n.workflow && <>workflow {n.workflow}</>}
                  {n.wait?.event && <>wait for {shown(n.wait.event)} (up to {n.wait.timeout})</>}
                  {n.wait?.duration && <>wait {n.wait.duration}</>}
                  {n.approval && <>approval by someone holding {n.approval.permission}{n.approval.subject && `: ${n.approval.subject}`}</>}
                </td>
                <td>{n.after?.join(", ") || "—"}</td>
                <td>{n.when || "—"}</td>
                {revealed && <td>{n.input === undefined ? "—" : <pre className="json">{JSON.stringify(n.input, null, 2)}</pre>}</td>}
              </tr>
            ))}
          </tbody>
        </table>
        <p className="muted">Version {p.digest.slice(0, 12)}: approving approves exactly this version.</p>
      </section>
      {problems.length > 0 && (
        <section>
          <h2>{approvable ? "Problems" : "Why staff cannot approve it"}</h2>
          <ul>{problems.map((problem, i) => <li key={i}>{shown(problem)}</li>)}</ul>
          {!revealed && <p className="muted">Reveal the plan to read them.</p>}
        </section>
      )}
      <section>
        <h2>Decide</h2>
        {p.is_template ? (
          <>
            <p>Copy this template into a new draft for one firm; the draft is then reviewed and approved like any plan.</p>
            <ActionForm label="Copy into a draft" allowed={can.includes("platform:plans")} minLength={10} onSubmit={async (reason) => {
              const answer = await post<{ plan_id: number }>(`${base}/instantiate`, { firm: firm.trim(), reason });
              navigate(`/plans/${answer.plan_id}`);
            }}>
              <label>Firm ID<input value={firm} onChange={(event) => setFirm(event.target.value)} placeholder="org_…" required /></label>
            </ActionForm>
          </>
        ) : (
          <>
            {p.state === "draft" && (
              <details>
                <summary>Propose for approval</summary>
                <ActionForm label="Propose" minLength={10} onSubmit={async (reason) => { await post(`${base}/propose`, { reason }); plan.reload(); }} />
              </details>
            )}
            {(p.state === "draft" || p.state === "proposed") && (
              <details>
                <summary>Reject</summary>
                <ActionForm label="Reject plan" danger minLength={10} onSubmit={async (reason) => { await post(`${base}/reject`, { reason }); plan.reload(); }} />
              </details>
            )}
            {approvable && !revealed && <p className="note">Reveal the plan and review it before approving: approving starts it for the firm.</p>}
            {approvable && revealed && (
              <details open>
                <summary>Approve and start</summary>
                <p>Starts version {p.digest.slice(0, 12)} for {p.firm_name ?? p.organization} now.</p>
                <ActionForm label="Approve and start" allowed={can.includes("platform:content")} minLength={10} onSubmit={async (reason) => {
                  const answer = await post<{ run_id: number }>(`${base}/approve`, { digest: p.digest, reason });
                  navigate(`/runs/${answer.run_id}`);
                }} />
              </details>
            )}
            {!["draft", "proposed"].includes(p.state) && <p>This plan is {p.state}; nothing more to decide.</p>}
          </>
        )}
      </section>
    </>
  );
}
