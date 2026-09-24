import { PrepaidMethodFields, readCredits } from "../prepaids/MethodFields";
import { AccrualConfirmations } from "../accruals/Prepare";
import { EmailHistory, SendRequestEmail, type EmailRecord } from "./Email";
import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Avatar, Icon, type IconName } from "../dashboard/Icon";
import { SourceReview, type Source } from "./Evidence";
import { PlanChips, type Plan } from "./Planning";
import { workspaceRequest } from "./workspace";

type Fact = {
 optional?: boolean;
  field: string;
  label: string;
  value: string;
  question: string;
  candidates: {
    value: string;
    source_id: string;
    location: string;
    quote: string;
  }[];
};
type WorkItem = Plan & {
  id: string;
  close_id: string;
  entity: string;
  period: string;
  state: string;
  error: string;
  blockers: string[];
  agent_started_at: string;
  agent_attempts: number;
  agent_checkpoint: { stage?: string; input_tokens?: number; output_tokens?: number };
  revision: number;
  stale: boolean;
  coordinated: boolean;
  source_id: string;
  source_name: string;
  extraction_state: string;
  workpaper_ids: string[];
  proposal: {
    facts?: Fact[];
    sources?: Source[];
    kind?: string;
    engine?: string;
    summary?: string;
    questions?: string[];
    unselected?: Source[];
  };
  requests: {
    consolidated?: boolean;
    id: string;
    body: string;
    recipient: string;
    approved_by: string;
    delivered_at: string | null;
    delivery_reference: string | null;
    emails: EmailRecord[];
    email?: {
      delivery: string;
      id: string;
      state: string;
      provider_id: string | null;
      error: string;
    } | null;
    link?: {
      expires_at: string;
      revoked_at: string | null;
      used_at: string | null;
    } | null;
  }[];
  replies: {
    id: string;
    source_id: string;
    received_from: string;
    recorded_by: string;
    body: string;
  }[];
};
type Dependency = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; upstream_workpaper_id: string };
const labels: Record<string, string> = {
  waiting_evidence: "Reading evidence",
  needs_confirmation: "Needs your decision",
  waiting_client: "Client request",
  queued: "Preparation queued",
  interpreting: "Agent interpreting evidence",
  completed: "Prepared",
  failed: "Needs attention",
  cancelled: "Cancelled",
};

const views: [string, string][] = [
  ["active", "Active"],
  ["mine", "Mine"],
  ["needs_confirmation", "Decisions"],
  ["blocked", "Blockers"],
  ["running", "Running"],
  ["waiting_client", "Client requests"],
  ["completed", "Prepared"],
  ["all", "All"],
];
const tones: Record<string, string> = {
  needs_confirmation: "status warn",
  failed: "status danger",
  waiting_client: "status info",
  queued: "status info",
  interpreting: "status info",
  waiting_evidence: "status info",
  completed: "status ok",
};
type QueueItem = Plan & { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; created_at: string; digest: string; review_id: string; bulk_reason: string };
const kindLabels: Record<string, string> = { prepaid: "Prepaid schedule", accrual: "Accrued expense", reconciliation: "Statement reconciliation", settlement: "Stripe settlement" };

function Kpi({ icon, tone, label, value, caption, onClick }: { icon: IconName; tone: string; label: string; value?: number; caption: string; onClick: () => void }) {
  return (
    <button type="button" className="kpi" onClick={onClick}>
      <span className="kpi-head">
        <span className={`kpi-icon ${tone}`}>
          <Icon name={icon} size={16} />
        </span>
        {label}
      </span>
      <span className="kpi-body">
        <span className="kpi-value">{value ?? "–"}</span>
        <span className="kpi-caption">{caption}</span>
      </span>
    </button>
  );
}

export function Work({
  closeId,
  onPrepared,
}: {
  closeId?: string;
  onPrepared?: () => void;
}) {
  const [items, setItems] = useState<WorkItem[]>([]);
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [more, setMore] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [filter, setFilter] = useState(closeId ? "all" : "active");
  const [clientSearch, setClientSearch] = useState("");
  const [dependencies, setDependencies] = useState<Dependency[]>([]);
  const [dependencyMore, setDependencyMore] = useState(false);
  const [dependencyError, setDependencyError] = useState("");
  const [dependencyLoaded, setDependencyLoaded] = useState(false);
  const [queue, setQueue] = useState<{ items: QueueItem[]; count: number }>();
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try {
        const data = await workspaceRequest(
          `${closeId ? `/${closeId}/work` : "/work"}?view=${filter}&client=${encodeURIComponent(clientSearch)}`,
        );
        if (!live) return;
        setItems(data.items);
        setCounts(data.counts ?? {});
        setMore(data.has_more);
        setError("");
        setLoaded(true);
        timer = setTimeout(load, 3000);
      } catch (e) {
        if (live)
          setError(e instanceof Error ? e.message : "Inbox unavailable.");
      }
    }
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [closeId, refresh, filter, clientSearch]);
  useEffect(() => {
    if (closeId) return;
    let live = true;
    setDependencyLoaded(false);
    setDependencies([]);
    setDependencyMore(false);
    setDependencyError("");
    workspaceRequest(`/dependencies?client=${encodeURIComponent(clientSearch)}`).then(data => {
      if (live) { setDependencies(data.items); setDependencyMore(data.has_more); setDependencyError(""); setDependencyLoaded(true); }
    }).catch(e => {
      if (live) { setDependencyError(e instanceof Error ? e.message : "Dependency review unavailable."); setDependencyLoaded(true); }
    });
    workspaceRequest(`/review-queue?client=${encodeURIComponent(clientSearch)}`).then(data => live && setQueue(data)).catch(() => live && setQueue(undefined));
    return () => { live = false; };
  }, [closeId, clientSearch, refresh]);
  const show = (view: string) => {
    setFilter(view);
    document.getElementById("work-list")?.scrollIntoView({ block: "start" });
  };
  return (
    <section
      id={closeId ? "close-work" : undefined}
      aria-label="Live Work inbox"
      className={closeId ? undefined : "bare"}
    >
      {closeId ? (
        <>
          <h2>Work from your evidence</h2>
          <p>
            Upload evidence to this close. Review proposed facts and questions,
            confirm assumptions, then preparation runs automatically.
          </p>
        </>
      ) : (
        <>
          <div className="page-title">
            <div>
              <span className="kpi-icon" aria-hidden="true">
                <Icon name="work" />
              </span>
              <span>
                <h1>Work</h1>
                <p>What needs you, what the agent is handling, and what can finish next.</p>
              </span>
            </div>
            <form
              className="title-actions"
              role="search"
              onSubmit={(e) => {
                e.preventDefault();
                setClientSearch(String(new FormData(e.currentTarget).get("client") ?? ""));
              }}
            >
              <input name="client" aria-label="Filter by client or close month" placeholder="Client or YYYY-MM" maxLength={200} />
              <button>Filter</button>
              <Link className="button primary" to="/dashboard/closes">
                Open a client close
              </Link>
            </form>
          </div>
          <div className="kpis">
            <Kpi icon="decision" tone="amber" label="Needs your decision" value={counts.needs_confirmation} caption="Proposed facts and questions to confirm" onClick={() => show("needs_confirmation")} />
            <Kpi icon="review" tone="" label="Ready for review" value={queue?.count} caption="Prepared drafts with evidence and no blockers" onClick={() => document.getElementById("review-queue")?.scrollIntoView({ block: "start" })} />
            <Kpi icon="waiting" tone="green" label="Waiting on clients" value={counts.waiting_client} caption="Approved requests awaiting a reply" onClick={() => show("waiting_client")} />
            <Kpi icon="accrual" tone="gray" label="Due in 7 days" value={counts.due_soon} caption="Closes due within a week, including overdue" onClick={() => show("active")} />
            <Kpi icon="blocked" tone="red" label="Blocked" value={counts.blocked} caption="Failed, replaced or unsupported work" onClick={() => show("blocked")} />
          </div>
        </>
      )}
      {error && (
        <p role="alert">{error} Updates paused; refresh to reconnect.</p>
      )}
      {!loaded && !error && <p role="status">Loading work…</p>}
      {loaded && (
        <>
          {!closeId && <PortfolioCommands key={`${filter}:${clientSearch}`} items={items} hasMore={more} onFilter={show} onChange={() => setRefresh((n) => n + 1)} />}
          {!closeId && (
            <>
              <div className="section-title" id="review-queue">
                <h2>Ready for your review</h2>
                {queue && queue.count > queue.items.length && <small>Showing {queue.items.length} of {queue.count}</small>}
              </div>
              <BulkReview items={queue?.items} onDone={() => setRefresh((n) => n + 1)} />
              <div className="section-title" id="dependency-review">
                <h2>Work needing re-review</h2>
                <small>Source revisions and inherited schedules reopen review; earlier approvals are not reused.</small>
              </div>
              <div className="rows" aria-label="Portfolio dependency review">
                {!dependencyLoaded && <p role="status" className="empty">Checking dependencies…</p>}
                {dependencyError && <p role="alert" className="empty">{dependencyError}</p>}
                {dependencyLoaded && !dependencyError && dependencies.length === 0 && <p className="empty">No current workpapers have a changed dependency in this client filter.</p>}
                {dependencies.map(item => (
                  <Link className="row" key={item.workpaper_id} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
                    <span className="row-main">
                      <Avatar label={item.entity} />
                      <span>
                        <strong>{item.entity} · {item.period}</strong>
                        <small>{kindLabels[item.kind] ?? item.kind} · draft #{item.workpaper_id}</small>
                      </span>
                    </span>
                    <span className="status danger">Evidence changed</span>
                    <small>{item.upstream_workpaper_id ? `Linked to prior draft #${item.upstream_workpaper_id}` : "Linked source changed"}</small>
                    <Icon name="arrow" size={16} />
                  </Link>
                ))}
              </div>
              {dependencyMore && <p>Showing 100 recent affected workpapers. Filter by client or month to see more.</p>}
            </>
          )}
          <div className="toolbar" id="work-list">
            <div className="segmented" role="group" aria-label="Work view">
              {views.map(([key, label]) => (
                <button key={key} type="button" aria-pressed={filter === key} onClick={() => setFilter(key)}>
                  {label}
                  {counts[key] !== undefined && <span className={key === "needs_confirmation" && counts[key] > 0 ? "count alert" : "count"}>{counts[key]}</span>}
                </button>
              ))}
            </div>
            <button type="button" onClick={() => setRefresh((n) => n + 1)}>Refresh</button>
          </div>
          {items.length === 0 && (
            <p className="empty">
              No work matches this view. Change the filters or upload evidence
              in a client close.
            </p>
          )}
          <fieldset disabled={!!error} className="rows work-rows">
            {items.map((item) => (
              <details
                key={item.id}
                id={`work-${item.id}`}
                className="work-item"
              >
                <summary className="row">
                  <span className="row-main">
                    <Avatar label={item.entity} />
                    <span>
                      <strong>{item.entity} · {item.period}</strong>
                      <small>{item.source_name} · #{item.id}</small>
                    </span>
                  </span>
                  <span className={item.stale || item.blockers.length > 0 ? "status danger" : (tones[item.state] ?? "status")}>
                    {item.stale
                      ? "Source replaced"
                      : item.blockers.length > 0
                        ? "Prepared · review blockers"
                        : (labels[item.state] ?? item.state)}
                  </span>
                  <span>
                    <small>{item.proposal.kind && item.proposal.kind !== "unsupported" ? (kindLabels[item.proposal.kind] ?? item.proposal.kind) : ""}</small> <PlanChips plan={item} />
                  </span>
                  <span className="chip">Open</span>
                </summary>
                <div className="work-body">
                  <WorkDetails
                    key={`${item.id}:${item.revision}`}
                    item={item}
                    onPrepared={onPrepared}
                    onChange={() => setRefresh((n) => n + 1)}
                  />
                </div>
              </details>
            ))}
          </fieldset>
          {more && (
            <p>
              Showing the first 100 matching jobs, with decisions first. Filter
              by client or close month to narrow the inbox.
            </p>
          )}
        </>
      )}
    </section>
  );
}

function BulkReview({ items, onDone }: { items?: QueueItem[]; onDone: () => void }) {
  const [selected, setSelected] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<QueueItem[]>();
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ workpaper_id: string; review_id?: string; error?: string }[]>([]);
  const [error, setError] = useState("");
  const chosen = confirming ?? [];
  const label = (item: QueueItem) => `${item.entity} · ${item.period} · ${kindLabels[item.kind] ?? item.kind} #${item.workpaper_id}`;
  async function approve() {
    setBusy(true);
    setError("");
    try {
      const data = await workspaceRequest("/review-batch", {
        note,
        items: chosen.map((item) => ({ close_id: item.close_id, workpaper_id: item.workpaper_id, digest: item.digest, expected_review_id: item.review_id })),
      });
      setResults(data.results);
      setSelected([]);
      setConfirming(undefined);
      setNote("");
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Bulk approval failed. Nothing was approved by this request.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <div className="rows">
        {!items && <p className="empty">Review queue unavailable. Open a close to review its drafts.</p>}
        {items && items.length === 0 && <p className="empty">Nothing is waiting for review in this filter.</p>}
        {items?.map((item) => (
          <div className="row" key={item.workpaper_id}>
            <span className="row-main">
              <input
                type="checkbox"
                aria-label={`Select ${label(item)} for bulk approval`}
                disabled={!!item.bulk_reason || busy || (!selected.includes(item.workpaper_id) && selected.length >= 20)}
                checked={selected.includes(item.workpaper_id)}
                onChange={(e) => {
                  setConfirming(undefined);
                  setSelected((current) => (e.target.checked ? [...current, item.workpaper_id] : current.filter((id) => id !== item.workpaper_id)));
                }}
              />
              <Avatar label={item.entity} />
              <span>
                <strong>{item.entity} · {item.period}</strong>
                <small>
                  {kindLabels[item.kind] ?? item.kind} · draft #{item.workpaper_id}
                  {item.bulk_reason ? ` · review individually: ${item.bulk_reason.toLowerCase()}` : ""}
                </small>
              </span>
            </span>
            <span className="status warn">Awaiting review</span>
            <PlanChips plan={item} />
            <Link className="button" to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
              Review
            </Link>
          </div>
        ))}
      </div>
      {selected.length > 0 && !confirming && (
        <div className="toolbar">
          <button type="button" className="primary" onClick={() => setConfirming((items ?? []).filter((item) => selected.includes(item.workpaper_id)))}>
            Approve {selected.length} selected
          </button>
          <button type="button" onClick={() => setSelected([])}>
            Clear selection
          </button>
        </div>
      )}
      {confirming && (
        <section aria-label="Confirm bulk approval">
          <h3>Approve {chosen.length === 1 ? "1 draft" : `${chosen.length} drafts`}</h3>
          <table>
            <thead>
              <tr>
                <th>Draft</th>
                <th>Effect</th>
                <th>Exceptions</th>
              </tr>
            </thead>
            <tbody>
              {chosen.map((item) => (
                <tr key={item.workpaper_id}>
                  <th>{label(item)}</th>
                  <td>No ledger change</td>
                  <td>None: evidence linked, no blockers, questions or reviewer classifications</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            <strong>Total effect:</strong> no ledger changes. Approval records a review decision on each exact draft; it is not posting or sign-off. You approve as yourself and need the reviewer role. Each draft is rechecked individually, so some can succeed while others are refused.
          </p>
          <label>
            What did you check across these drafts?
            <textarea required maxLength={2000} value={note} onChange={(e) => setNote(e.target.value)} />
          </label>
          <div className="toolbar">
            <button type="button" className="primary" disabled={busy || !note.trim()} onClick={() => void approve()}>
              {busy ? "Approving…" : `Approve ${chosen.length === 1 ? "1 draft" : `${chosen.length} drafts`}`}
            </button>
            <button type="button" disabled={busy} onClick={() => setConfirming(undefined)}>
              Back
            </button>
          </div>
          {error && <p role="alert">{error}</p>}
        </section>
      )}
      {results.length > 0 && (
        <section role="status" aria-label="Bulk approval results">
          <h3>Approval results</h3>
          <ul>
            {results.map((result) => (
              <li key={result.workpaper_id}>
                Draft #{result.workpaper_id}: {result.review_id ? "approved" : result.error}
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}

function PortfolioCommands({items,hasMore,onFilter,onChange}:{items:WorkItem[];hasMore:boolean;onFilter:(view:string)=>void;onChange:()=>void}) {
  const [mode,setMode] = useState<"prepare"|"stop"|"recheck">("prepare");
  const [command,setCommand] = useState("");
  const [answer,setAnswer] = useState("");
  const [answerClose,setAnswerClose] = useState("");
  const closesFor = (action:"prepare"|"stop"|"recheck") => [...new Map(items.filter(i=>action === "prepare" || (action === "recheck" ? ["failed","needs_confirmation"].includes(i.state) : !["completed","cancelled"].includes(i.state))).map(i=>[i.close_id,{id:i.close_id,label:`${i.entity} · ${i.period}`,entity:i.entity.toLowerCase()}])).values()];
  const closes = closesFor(mode);
  const [selected,setSelected] = useState<string[]>([]);
  const [preview,setPreview] = useState<{id:string;label:string;count:number;digest:string}[]>();
  const [reason,setReason] = useState("");
  const [busy,setBusy] = useState(false);
  const [error,setError] = useState("");
  const [outcomes,setOutcomes] = useState<string[]>([]);
  async function inspect(targets = selected, action = mode) {
    setBusy(true);setError("");setOutcomes([]);
    try {
      const next = [];
      for (const close of closesFor(action).filter(c=>targets.includes(c.id))) {
        const status = await workspaceRequest(`/${close.id}/prepare-close`);
        next.push({...close,count:action === "prepare" ? status.inventory.length as number : action === "recheck" ? status.recheck.count as number : status.stop.count as number,digest:action === "prepare" ? status.inventory_digest as string : action === "recheck" ? status.recheck.digest as string : status.stop.digest as string});
      }
      setPreview(next);
    } catch(e) { setPreview(undefined);setError(e instanceof Error?e.message:"Could not inspect selected closes."); }
    finally { setBusy(false); }
  }
  function interpret() {
    setAnswer("");setAnswerClose("");setError("");
    const view = /^(?:show|list)(?: me)? (decisions|blockers|running work|client requests|prepared work|all work|dependencies)$/i.exec(command.trim());
    if (view) {
      if (view[1].toLowerCase() === "dependencies") {setAnswer("Changed workpapers are listed in Work needing re-review.");return;}
      const views:Record<string,string> = {decisions:"needs_confirmation",blockers:"blocked","running work":"running","client requests":"waiting_client","prepared work":"completed","all work":"all"};
      onFilter(views[view[1].toLowerCase()]);
      return;
    }
    const rehearsal = /^rehearse (.+)$/i.exec(command.trim());
    if (rehearsal) {
      const query = rehearsal[1].trim().toLowerCase();
      setBusy(true);
      void workspaceRequest("").then(data => {
        const matches = (data.closes as {id:string;entity:string;period:string}[]).filter(c=>c.entity.toLowerCase() === query || `${c.entity} · ${c.period}`.toLowerCase() === query);
        if(matches.length !== 1) {setError(data.has_more ? "Name an exact client and month among the latest 100 closes, or open Client closes." : "Name exactly one accessible client and month for rehearsal.");return;}
        setAnswer(`Open the saved production journal rehearsal for ${matches[0].entity} · ${matches[0].period}.`);
        setAnswerClose(matches[0].id);
      }).catch(e=>setError(e instanceof Error ? e.message : "Could not find that close.")).finally(()=>setBusy(false));
      return;
    }
    const why = /^why is (.+) waiting\??$/i.exec(command.trim());
    if (why) {
      const query = why[1].trim().toLowerCase();
      const matches = [...new Map(items.filter(i=>i.entity.toLowerCase() === query || `${i.entity} · ${i.period}`.toLowerCase() === query).map(i=>[i.close_id,i])).values()];
      if (matches.length !== 1) {setError("Name exactly one visible client and month to explain its work.");return;}
      const related = items.filter(i=>i.close_id === matches[0].close_id);
      setError("");setAnswer(related.map(i=>`${i.source_name}: ${labels[i.state] ?? i.state}${i.blockers.length ? ` — ${i.blockers.join("; ")}` : i.error ? ` — ${i.error}` : ""}`).join("\n"));
      return;
    }
    const match = /^(prepare|recheck|stop) (.+)$/i.exec(command.trim());
    if (!match) {setError("Try ‘show blockers’, ‘rehearse Client · YYYY-MM’, ‘prepare all visible’ or ‘why is Client · YYYY-MM waiting?’.");return;}
    const action = match[1].toLowerCase() as "prepare"|"stop"|"recheck";
    const query = match[2].trim().toLowerCase();
    const eligible = closesFor(action);
    if (query === "all visible" || query === "visible closes") {
      if (hasMore || eligible.length > 10) {setError("Narrow the inbox to at most 10 visible closes before a portfolio command.");return;}
      if (eligible.length === 0) {setError("No eligible visible closes match this command.");return;}
      setMode(action);setSelected(eligible.map(c=>c.id));setPreview(undefined);void inspect(eligible.map(c=>c.id),action);
      return;
    }
    const matches = eligible.filter(c=>c.entity === query || c.label.toLowerCase() === query);
    if (matches.length !== 1) {setError(matches.length ? "Several closes match. Include the close month shown below." : "No eligible visible close matches. Change the inbox filter or use the exact client name and month.");return;}
    setMode(action);setSelected([matches[0].id]);setPreview(undefined);setError("");
    void inspect([matches[0].id],action);
  }
  async function submit() {
    if(!preview?.length || (mode === "stop" && !reason.trim())) return;
    setBusy(true);setError("");
    const results:string[]=[];
    for(const close of preview) {
      if(close.count===0) { results.push(`${close.label}: no ${mode === "prepare" ? "source jobs to coordinate" : mode === "recheck" ? "ready unresolved jobs to recheck" : "unresolved jobs to stop"}.`); continue; }
      try {
        if(mode === "prepare") await workspaceRequest(`/${close.id}/prepare-close`,{inventory_digest:close.digest,confirmed:true});
        else if(mode === "recheck") await workspaceRequest(`/${close.id}/prepare-close/recheck`,{digest:close.digest,confirmed:true});
        else await workspaceRequest(`/${close.id}/prepare-close/cancel`,{digest:close.digest,reason:reason.trim(),confirmed:true});
        results.push(`${close.label}: ${mode === "prepare" ? "coordinated" : mode === "recheck" ? "rechecking" : "stopped"} ${close.count} source job${close.count===1?"":"s"}.`);
      } catch(e) { results.push(`${close.label}: ${e instanceof Error?e.message:"Command failed"}. Inspect this close before another attempt.`); }
    }
    setOutcomes(results);setPreview(undefined);setSelected([]);setBusy(false);onChange();
  }
  return <details open className="agent-card"><summary><Icon name="agent" /> Ask the agent · find, explain and preview actions</summary>
    <p>Select up to 10 visible closes. Each close is checked again when submitted and reports its own result. Preparing coordinates existing source jobs; facts and methods still need separate review.</p>
    <div className="ask-row"><label>Ask Work<input value={command} onChange={e=>setCommand(e.target.value)} onKeyDown={e=>{if(e.key==="Enter"){e.preventDefault();interpret();}}} placeholder="show blockers · prepare all visible · rehearse Client · YYYY-MM" maxLength={220} /></label><button type="button" className="primary" disabled={busy || !command.trim()} onClick={interpret}>Find and preview</button></div>
    <p>Try “show decisions”, “show dependencies”, “why is Client · YYYY-MM waiting?”, “rehearse Client · YYYY-MM”, or “prepare/recheck/stop all visible”. Actions always need a preview and confirmation.</p>
    {answer && <p role="status" style={{whiteSpace:"pre-line"}}>{answer}</p>}
    {answerClose && <Link to={`/dashboard/closes/${answerClose}`}>Open close rehearsal</Link>}
    <details><summary>Choose closes for a portfolio action</summary>
    <label>Command<select value={mode} onChange={e=>{setMode(e.target.value as "prepare"|"stop"|"recheck");setSelected([]);setPreview(undefined);setOutcomes([]);}}><option value="prepare">Prepare selected closes</option><option value="recheck">Recheck failed or pending proposals</option><option value="stop">Stop unresolved work</option></select></label>
    {closes.map(close=><label key={close.id}><input type="checkbox" checked={selected.includes(close.id)} disabled={busy || (!selected.includes(close.id) && selected.length>=10)} onChange={e=>{setPreview(undefined);setOutcomes([]);setSelected(current=>e.target.checked?[...current,close.id]:current.filter(id=>id!==close.id));}} /> {close.label}</label>)}
    <button type="button" disabled={busy || selected.length===0} onClick={()=>void inspect(selected,mode)}>{busy?"Checking…":`Preview ${selected.length} selected close${selected.length===1?"":"s"}`}</button>
    </details>
    {preview && <div><h3>Exact {mode} preview</h3><ul>{preview.map(close=><li key={close.id}>{close.label}: {close.count} {mode === "prepare" ? "source" : "unresolved"} job{close.count===1?"":"s"}</li>)}</ul>{mode === "stop" && <label>Reason for stopping<textarea value={reason} onChange={e=>setReason(e.target.value)} maxLength={500} /></label>}<button type="button" disabled={busy || (mode === "stop" && !reason.trim()) || preview.every(close=>close.count===0)} onClick={()=>void submit()}>{mode === "prepare" ? "Coordinate selected closes" : mode === "recheck" ? "Recheck listed proposals" : "Stop listed work"}</button></div>}
    {outcomes.length>0 && <div role="status"><h3>Result by close</h3><ul>{outcomes.map((result,i)=><li key={i}>{result}</li>)}</ul></div>}
    {error && <p role="alert">{error}</p>}
  </details>;
}

function WorkDetails({
  item,
  onChange,
  onPrepared,
}: {
  item: WorkItem;
  onChange: () => void;
  onPrepared?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [replyLink, setReplyLink] = useState("");
  const [error, setError] = useState("");
  const facts = item.proposal.facts ?? [];
  const kind = item.proposal.kind ?? "prepaid";
  const questions = item.proposal.questions ?? [];
  const latest = item.requests[0];
  const [drafts, setDrafts] = useState<
    { id: string; body: string; stale: boolean }[]
  >([]);
  const [selectedDraft, setSelectedDraft] = useState("");
  const [requestBody, setRequestBody] = useState(
    `For ${item.entity} · ${item.period}, regarding ${item.source_name}:\n\n${
      [
        ...questions,
        ...facts.filter((f) => f.question).map((f) => f.question),
      ].join("\n") ||
      "Please clarify the following specific missing information:"
    }`,
  );
  const path = `/${item.close_id}/work/${item.id}`;
  async function act(action: string, body: object = {}) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`${path}/${action}`, {
        revision: item.revision,
        ...body,
      });
      if (result.link_token)
        setReplyLink(`${window.location.origin}/request#${result.link_token}`);
      if (action === "revoke_link") setReplyLink("");
      onChange();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Update failed.");
    } finally {
      setBusy(false);
    }
  }
  function confirm(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const input: Record<string, unknown> = Object.fromEntries(
      facts.map((f) => [f.field, String(data.get(f.field) ?? "")]),
    );
    if (kind === "reconciliation") {
      Object.assign(input, {
        expected_pages: Number(input.expected_pages),
        pages: String(input.pages ?? "")
          .split(",")
          .map((n) => Number(n.trim())),
        policy_confirmed: data.has("method"),
      });
    } else {
      Object.assign(input, {
        basis: "accrual",
        method: String(data.get("method")),
        method_confirmed: data.has("method_confirmed"),
        booking_confirmed: data.has("booking_confirmed"),
        examples_confirmed:data.has("examples_confirmed"),
        ...(kind==="accrual" ? {reverse_next_month:data.get("reverse_next_month")==="true",estimate_amount:String(data.get("estimate_amount")??""),estimate_reference:String(data.get("estimate_reference")??""),actual_reference:String(data.get("actual_reference")??""),accountant_rule_reference:String(data.get("accountant_rule_reference")??"")} : {...Object.fromEntries(["credit_amount","credit_period","credit_reference","booked_balance","booking_reference","cancellation_method","cancellation_period","cancellation_reference","accountant_rule_reference"].map(key=>[key,String(data.get(key)??"")])),credits:readCredits(data)}),
      });
    }
    void act("confirm", { input, facts_confirmed: data.has("facts"), inventory_confirmed:data.has("inventory") });
  }
  return (
    <div>
      <p>
        <Link to={`/dashboard/closes/${item.close_id}`}>
          Open {item.entity} · {item.period}
        </Link>
      </p>
      <p>
        Durable job #{item.id} · revision {item.revision}. Preparation does not
        approve or post a journal.
      </p>
      {item.coordinated && <p>Agent budget: {item.agent_attempts} of 3 interpretation attempts used. Checkpoint: {item.agent_checkpoint?.stage?.replaceAll("_", " ") ?? "pending"}. Each attempt selects at most 20 sources / 128 KiB and requests at most 8,000 output tokens across selection and interpretation; a fresh client answer or reviewer recheck can reset this budget.</p>}
      {item.error && <p role="alert">{item.error}</p>}
      {item.blockers?.length > 0 && (
        <section>
          <h3>Unresolved blockers</h3>
          <ul>
            {item.blockers.map((b, i) => (
              <li key={i}>{b}</li>
            ))}
          </ul>
        </section>
      )}
      {item.state === "interpreting" && (
        <p role="status">
          The agent is inspecting permitted evidence and proposing supported
          work. Started {new Date(item.agent_started_at).toLocaleString()}. No
          calculation or posting is authorized.
        </p>
      )}
      {item.stale && (
        <p role="alert">
          This source was replaced. Use the replacement’s work item; this job
          cannot prepare from old evidence.
        </p>
      )}
      {(item.proposal.sources?.length ?? 0) > 0 && (
        <SourceReview
          closeId={item.close_id}
          sources={item.proposal.sources ?? []}
        />
      )}
      {item.state === "waiting_evidence" && (
        <p role="status">
          Waiting for the worker to read evidence and propose facts. PDF
          extraction: {item.extraction_state}.
        </p>
      )}
      {item.state === "queued" && (
        <p role="status">
          Confirmed instructions are queued for the preparation worker.
        </p>
      )}
      {item.state === "completed" && (
        <p>
          Prepared draft{item.workpaper_ids.length === 1 ? "" : "s"}{" "}
          {item.workpaper_ids.map((id) => `#${id}`).join(", ")}.{" "}
          <Link onClick={onPrepared} to={`/dashboard/closes/${item.close_id}`}>
            Review the results and source evidence
          </Link>
          .
        </p>
      )}
      {item.state === "needs_confirmation" && !item.stale && (
        <>
          <h3>
            {item.proposal.engine === "openai"
              ? "Agent-proposed work and facts"
              : item.proposal.engine === "recognized_export"
                ? kind === "reconciliation" ? "Recognized reconciliation CSV" : "Recognized Stripe payout export"
                : "Proposed facts · local extraction"}
          </h3>
          {item.proposal.summary && <p>{item.proposal.summary}</p>}
          {questions.length > 0 && (
            <section>
              <h4>Information needed</h4>
              <ul>
                {questions.map((q, i) => (
                  <li key={i}>{q}</li>
                ))}
              </ul>
            </section>
          )}
          {item.proposal.engine === "local" && (
            <p>
              AI interpretation is not configured. Only exact labeled lines are
              proposed; these are unverified candidates, not conclusions. An
              unlabeled invoice needs manual entry. Conflicting values remain
              blank.
            </p>
          )}
          {item.proposal.engine === "identity_blocked" && <p role="alert">This evidence names a different or ambiguous client. Compare it with the original and replace the misfiled source before preparing this work.</p>}
          {item.proposal.engine === "local" && (
            <p>
              For statement/ledger evidence,{" "}
              <Link
                to={`/dashboard/reconciliations/new?close=${item.close_id}&entity=${encodeURIComponent(item.entity)}&period=${item.period}&work=${item.id}&revision=${item.revision}`}
              >
                confirm reconciliation inputs and continue this job
              </Link>
              .
            </p>
          )}
          {item.proposal.engine === "openai" && (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void act("answer", {
                  body: new FormData(e.currentTarget).get("answer"),
                });
              }}
            >
              <fieldset disabled={busy}>
                <label>
                  Answer or clarify the proposed work
                  <textarea name="answer" required maxLength={20000} rows={4} />
                </label>
                <p>
                  Saved as preparer evidence. The agent rechecks the same job
                  against your answer and current sources.
                </p>
                <button>Save answer and resume agent</button>
              </fieldset>
            </form>
          )}
          {kind === "settlement" && (
            <p>
              <Link
                className="button primary"
                to={`/dashboard/settlements/new?close=${item.close_id}&entity=${encodeURIComponent(item.entity)}&period=${item.period}&work=${item.id}&revision=${item.revision}&report=${item.source_id}`}
              >
                Trace payouts to bank deposits →
              </Link>
            </p>
          )}
          {kind === "reconciliation" && item.proposal.engine === "recognized_export" && (
            <p>
              <Link
                className="button primary"
                to={`/dashboard/reconciliations/new?close=${item.close_id}&entity=${encodeURIComponent(item.entity)}&period=${item.period}&work=${item.id}&revision=${item.revision}`}
              >
                Choose statement or ledger and reconcile →
              </Link>
            </p>
          )}
          {kind !== "unsupported" && kind !== "settlement" && item.proposal.engine !== "recognized_export" && (
            <form onSubmit={confirm}>
              <fieldset disabled={busy}>
                <legend>
                  {kind === "reconciliation"
                    ? "Confirm proposed statement reconciliation"
                    : kind === "accrual" ? "Confirm proposed accrual" : "Confirm proposed prepaid preparation"}
                </legend>
                {facts.filter(f=>!f.optional).map((f) => (
                  <div key={f.field}>
                    <label>
                      {f.label}
                      <input
                        name={f.field}
                        type={f.field.startsWith("service_") ? "date" : "text"}
                        defaultValue={f.value}
                        required
                        maxLength={200}
                      />
                    </label>
                    {f.question && <p>{f.question}</p>}
                    {f.candidates.map((c, i) => (
                      <p className="source-label" key={i}>
                        Evidence #{c.source_id}, {c.location}: <q>{c.quote}</q>
                      </p>
                    ))}
                  </div>
                ))}
                <label>
                  <input name="facts" type="checkbox" required /> I checked all
                  facts and replies against the originals, resolved
                  contradictions, and confirmed this evidence belongs to{" "}
                  {item.entity}.
                </label>
                {!!item.proposal.unselected?.length && <details open><summary>{item.proposal.unselected.length} sources not cited by this proposal</summary><p>The agent did not cite these files; on large closes it may not have read their full text. Inspect them before confirming that none changes this work:</p><ul>{item.proposal.unselected.map(s=><li key={s.id}>Evidence #{s.id}: {s.name}</li>)}</ul><label><input name="inventory" type="checkbox" required /> I reviewed the uncited evidence for this work.</label></details>}
                {kind==="reconciliation" ? <label><input name="method" type="checkbox" required /> Confirm USD, complete page inventory, exact-date/signed-amount matching; ambiguous transactions stay unresolved.</label> : kind==="accrual" ? <>{facts.filter(f=>f.optional && f.candidates.length>0).map(f=><p key={f.field}>{f.label}: {f.candidates.map(c=>`${c.value} · evidence #${c.source_id} ${c.location}: ${c.quote}`).join("; ")}</p>)}<AccrualConfirmations /></> : <>
                  {facts.filter(f=>f.optional && f.candidates.length>0).map(f=><p key={f.field}>{f.label}: {f.candidates.map(c=>`${c.value} · evidence #${c.source_id} ${c.location}: ${c.quote}`).join("; ")}{f.question}</p>)}
                  <PrepaidMethodFields values={Object.fromEntries(facts.map(f=>[f.field,f.value]))} />
                </>}
                <button
                  className="primary"
                  disabled={
                    item.proposal.engine === "openai" && questions.length > 0
                  }
                >
                  Confirm assumptions and prepare automatically
                </button>
              </fieldset>
            </form>
          )}
          <details>
            <summary>Need information from the client?</summary>
            <p>
              Review the exact recipient and wording. Approval stores the
              request; nothing is sent from this application.
            </p>
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const d = new FormData(e.currentTarget);
                void act("request", {
                  recipient: d.get("recipient"),
                  body: d.get("body"),
                  draft_id: selectedDraft,
                });
              }}
            >
              <fieldset disabled={busy}>
                <label>
                  Recipient email
                  <input
                    type="email"
                    name="recipient"
                    required
                    maxLength={254}
                  />
                </label>
                <button
                  type="button"
                  onClick={async () => {
                    try {
                      setDrafts(
                        await workspaceRequest(`/${item.close_id}/requests`),
                      );
                    } catch (e) {
                      setError(
                        e instanceof Error ? e.message : "Drafts unavailable.",
                      );
                    }
                  }}
                >
                  Load saved request drafts
                </button>
                {drafts.length > 0 && (
                  <label>
                    Use an existing request
                    <select
                      value={selectedDraft}
                      onChange={(e) => {
                        setSelectedDraft(e.target.value);
                        const draft = drafts.find(
                          (d) => d.id === e.target.value,
                        );
                        if (draft) setRequestBody(draft.body);
                      }}
                    >
                      <option value="">New wording</option>
                      {drafts.map((d) => (
                        <option key={d.id} value={d.id} disabled={d.stale}>
                          Draft #{d.id}
                          {d.stale ? " · refresh its context first" : ""}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <label>
                  Exact request
                  <textarea
                    name="body"
                    required
                    maxLength={20000}
                    rows={8}
                    value={requestBody}
                    onChange={(e) => {
                      setRequestBody(e.target.value);
                      setSelectedDraft("");
                    }}
                  />
                </label>
                <label>
                  <input type="checkbox" required /> I approve this exact
                  recipient and message for this client.
                </label>
                <button>Approve request · keep unsent</button>
              </fieldset>
            </form>
          </details>
        </>
      )}
      {item.requests.length > 0 && (
        <section>
          <h3>Approved requests</h3>
          {item.requests.map((q) => (
            <details key={q.id} open={q.id === latest.id}>
              <summary>
                Request #{q.id} ·{" "}
                {q.link?.used_at
                  ? "Reply received via private link"
                  : q.email
                    ? `Email ${q.email.state.replaceAll("_", " ")}`
                    : q.delivered_at
                      ? "Delivery recorded by staff"
                      : "Approved · not sent"}
              </summary>
              <p>
                To: {q.recipient} · approved by {q.approved_by}
              </p>
              <pre>{q.body}</pre>
              {!!q.emails?.length && <EmailHistory emails={q.emails} />}
              {q.delivery_reference && (
                <p>Delivery record: {q.delivery_reference}</p>
              )}
            </details>
          ))}
        </section>
      )}
      {item.state === "waiting_client" && latest?.consolidated && <p>Manage this consolidated request in <a href={`/dashboard/closes/${item.close_id}#coverage-work`}>Coverage and client evidence</a>.</p>}
      {item.state === "waiting_client" && latest && !latest.consolidated && !item.stale && (
        <fieldset disabled={busy}>
          {(!latest.email || (latest.email.state === "accepted" && latest.email.delivery === "delivered")) && !latest.link?.used_at && !latest.link?.revoked_at && (
            <SendRequestEmail
              key={latest.email?.id ?? "initial"}
              previousId={latest.email?.id}
              closeId={item.close_id}
              jobId={item.id}
              requestId={latest.id}
              revision={item.revision}
              recipient={latest.recipient}
              onQueued={onChange}
            />
          )}
          <section aria-label="Private reply link">
            <h3>Client reply link</h3>
            <p>
              Grants access only to this approved request for seven days. Share
              privately with {latest.recipient}. Anyone holding the link can
              reply; their identity is not verified. Creating a link does not
              send it.
            </p>
            {latest.link && (
              <p>
                {latest.link.revoked_at
                  ? "Link revoked."
                  : latest.link.used_at
                    ? "Reply received."
                    : `Last link expires ${new Date(latest.link.expires_at).toLocaleString()}.`}
              </p>
            )}
            <button onClick={() => void act("link", { request_id: latest.id })}>
              Create new private reply link
            </button>
            {latest.link && !latest.link.revoked_at && !latest.link.used_at && (
              <button
                onClick={() =>
                  void act("revoke_link", { request_id: latest.id })
                }
              >
                Revoke reply link
              </button>
            )}
            {replyLink && (
              <label>
                Copy this link now; it is not stored in readable form.
                <input
                  aria-label="Private reply URL"
                  readOnly
                  value={replyLink}
                  onFocus={(e) => e.currentTarget.select()}
                />
              </label>
            )}
            <p>
              A new link invalidates the previous one. Evidence changes require
              a fresh reviewed request.
            </p>
          </section>
          {!latest.delivered_at ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void act("delivered", {
                  request_id: latest.id,
                  delivery_reference: new FormData(e.currentTarget).get(
                    "reference",
                  ),
                });
              }}
            >
              <label>
                Delivery reference from your secure channel
                <input name="reference" required maxLength={500} />
              </label>
              <button>
                Record delivery already completed outside this app
              </button>
            </form>
          ) : (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                const d = new FormData(e.currentTarget);
                void act("reply", {
                  request_id: latest.id,
                  body: d.get("body"),
                  received_from: d.get("from"),
                });
              }}
            >
              <label>
                Reply received from
                <input name="from" required maxLength={254} />
              </label>
              <label>
                Original reply
                <textarea name="body" rows={6} required maxLength={20000} />
              </label>
              <p>
                Preserved as evidence. Candidate facts return for review on this
                same job; a reply cannot approve assumptions or posting.
              </p>
              <button>Record reply and resume this job</button>
            </form>
          )}
        </fieldset>
      )}
      {item.replies.length > 0 && (
        <section>
          <h3>Original replies · review required</h3>
          {item.replies.map((reply) => (
            <details key={reply.id}>
              <summary>
                Evidence #{reply.source_id} · reported sender{" "}
                {reply.received_from}
              </summary>
              <p>
                Recorded by {reply.recorded_by}; sender identity has not been
                independently verified.
              </p>
              <pre>{reply.body}</pre>
            </details>
          ))}
        </section>
      )}
      {item.coordinated &&
        ["failed", "needs_confirmation", "waiting_client", "cancelled"].includes(
          item.state,
        ) &&
        !item.stale && (
          <button disabled={busy} onClick={() => void act("retry")}>
            {item.state === "cancelled"
              ? "Restart this stopped job"
              : item.state === "waiting_client"
              ? "Withdraw request and recheck evidence"
              : "Recheck evidence and proposals"}
          </button>
        )}
      {item.coordinated &&
        !["completed", "cancelled", "queued"].includes(item.state) && (
          <button disabled={busy} onClick={() => void act("cancel")}>
            Cancel this preparation job
          </button>
        )}
      {error && <p role="alert">{error}</p>}
      {busy && <p role="status">Saving work…</p>}
    </div>
  );
}
