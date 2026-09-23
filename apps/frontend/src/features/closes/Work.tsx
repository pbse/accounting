import { PrepaidMethodFields } from "../prepaids/MethodFields";
import { AccrualConfirmations } from "../accruals/Prepare";
import { EmailHistory, SendRequestEmail, type EmailRecord } from "./Email";
import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { AccountAccess, authKitClientId } from "../auth/Auth";
import { SourceReview, type Source } from "./Evidence";
import { unlock, workspaceRequest } from "./workspace";

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
type WorkItem = {
  id: string;
  close_id: string;
  entity: string;
  period: string;
  state: string;
  error: string;
  blockers: string[];
  agent_started_at: string;
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

export function Work({
  closeId,
  onPrepared,
}: {
  closeId?: string;
  onPrepared?: () => void;
}) {
  const [items, setItems] = useState<WorkItem[]>([]);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [more, setMore] = useState(false);
  const [refresh, setRefresh] = useState(0);
  const [filter, setFilter] = useState(closeId ? "all" : "active");
  const [clientSearch, setClientSearch] = useState("");
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
  return (
    <section
      id={closeId ? "close-work" : undefined}
      aria-label="Live Work inbox"
    >
      {closeId ? (
        <h2>Work from your evidence</h2>
      ) : (
        <>
          <p className="eyebrow">LIVE WORK INBOX</p>
          <h1>What needs your attention?</h1>
        </>
      )}
      <p>
        Upload evidence to a client close. Review proposed facts and questions,
        confirm assumptions, then preparation runs automatically.
      </p>
      {!closeId && (
        <>
          <Link className="button primary" to="/dashboard/closes">
            Open or create a close · upload evidence
          </Link>
          {authKitClientId ? (
            <AccountAccess />
          ) : (
            <details open={!loaded}>
              <summary>Workspace access</summary>
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  unlock(String(new FormData(e.currentTarget).get("token")));
                  e.currentTarget.reset();
                  setRefresh((n) => n + 1);
                }}
              >
                <label>
                  Private workspace token
                  <input
                    name="token"
                    type="password"
                    autoComplete="off"
                    minLength={32}
                    required
                  />
                </label>
                <button>Unlock workspace</button>
              </form>
              <button
                onClick={() => {
                  unlock("");
                  setItems([]);
                  setLoaded(false);
                  setRefresh((n) => n + 1);
                }}
              >
                Lock workspace
              </button>
            </details>
          )}
        </>
      )}
      {!closeId && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setClientSearch(
              String(new FormData(e.currentTarget).get("client") ?? ""),
            );
          }}
        >
          <label>
            Client or close month
            <input
              name="client"
              placeholder="Search clients or YYYY-MM"
              maxLength={200}
            />
          </label>
          <button>Filter clients</button>
        </form>
      )}
      <button onClick={() => setRefresh((n) => n + 1)}>Refresh inbox</button>
      {error && (
        <p role="alert">{error} Updates paused; refresh to reconnect.</p>
      )}
      {!loaded && !error && <p role="status">Loading work…</p>}
      {loaded && (
        <>
          <label>
            Show
            <select value={filter} onChange={(e) => setFilter(e.target.value)}>
              <option value="active">Active work</option>
              <option value="needs_confirmation">Decisions</option>
              <option value="blocked">Blockers</option>
              <option value="running">Queued and running</option>
              <option value="waiting_client">Outstanding requests</option>
              <option value="completed">Prepared</option>
              <option value="all">All work</option>
            </select>
          </label>
          {items.length === 0 && (
            <p>
              No work matches this view. Change the filters or upload evidence
              in a client close.
            </p>
          )}
          {filter === "active" && (
            <p>
              <button onClick={() => setFilter("completed")}>
                View prepared work
              </button>
            </p>
          )}
          {!closeId && <PortfolioCommands key={`${filter}:${clientSearch}`} items={items} onChange={() => setRefresh((n) => n + 1)} />}
          <fieldset disabled={!!error}>
            {items.map((item) => (
              <details
                key={item.id}
                id={`work-${item.id}`}
                className="work-item"
              >
                <summary>
                  {item.entity} · {item.period} · {item.source_name} —{" "}
                  {item.stale
                    ? "Source replaced"
                    : item.blockers.length > 0
                      ? "Prepared · review blockers"
                      : (labels[item.state] ?? item.state)}{" "}
                  · #{item.id}
                </summary>
                <WorkDetails
                  key={`${item.id}:${item.revision}`}
                  item={item}
                  onPrepared={onPrepared}
                  onChange={() => setRefresh((n) => n + 1)}
                />
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

function PortfolioCommands({items,onChange}:{items:WorkItem[];onChange:()=>void}) {
  const [mode,setMode] = useState<"prepare"|"stop">("prepare");
  const closes = [...new Map(items.filter(i=>mode === "prepare" || !["completed","cancelled"].includes(i.state)).map(i=>[i.close_id,{id:i.close_id,label:`${i.entity} · ${i.period}`}])).values()];
  const [selected,setSelected] = useState<string[]>([]);
  const [preview,setPreview] = useState<{id:string;label:string;count:number;digest:string}[]>();
  const [reason,setReason] = useState("");
  const [busy,setBusy] = useState(false);
  const [error,setError] = useState("");
  const [outcomes,setOutcomes] = useState<string[]>([]);
  async function inspect() {
    setBusy(true);setError("");setOutcomes([]);
    try {
      const next = [];
      for (const close of closes.filter(c=>selected.includes(c.id))) {
        const status = await workspaceRequest(`/${close.id}/prepare-close`);
        next.push({...close,count:mode === "prepare" ? status.inventory.length as number : status.stop.count as number,digest:mode === "prepare" ? status.inventory_digest as string : status.stop.digest as string});
      }
      setPreview(next);
    } catch(e) { setPreview(undefined);setError(e instanceof Error?e.message:"Could not inspect selected closes."); }
    finally { setBusy(false); }
  }
  async function submit() {
    if(!preview?.length || (mode === "stop" && !reason.trim())) return;
    setBusy(true);setError("");
    const results:string[]=[];
    for(const close of preview) {
      if(close.count===0) { results.push(`${close.label}: no ${mode === "prepare" ? "source jobs to coordinate" : "unresolved jobs to stop"}.`); continue; }
      try {
        if(mode === "prepare") await workspaceRequest(`/${close.id}/prepare-close`,{inventory_digest:close.digest,confirmed:true});
        else await workspaceRequest(`/${close.id}/prepare-close/cancel`,{digest:close.digest,reason:reason.trim(),confirmed:true});
        results.push(`${close.label}: ${mode === "prepare" ? "coordinated" : "stopped"} ${close.count} source job${close.count===1?"":"s"}.`);
      } catch(e) { results.push(`${close.label}: ${e instanceof Error?e.message:"Command failed"}. Inspect this close before another attempt.`); }
    }
    setOutcomes(results);setPreview(undefined);setSelected([]);setBusy(false);onChange();
  }
  if(!closes.length) return null;
  return <details><summary>Portfolio commands · prepare or stop selected closes</summary>
    <p>Select up to 10 visible closes. Each close is checked again when submitted and reports its own result. Preparing coordinates existing source jobs; facts and methods still need separate review.</p>
    <label>Command<select value={mode} onChange={e=>{setMode(e.target.value as "prepare"|"stop");setSelected([]);setPreview(undefined);setOutcomes([]);}}><option value="prepare">Prepare selected closes</option><option value="stop">Stop unresolved work</option></select></label>
    {closes.map(close=><label key={close.id}><input type="checkbox" checked={selected.includes(close.id)} disabled={busy || (!selected.includes(close.id) && selected.length>=10)} onChange={e=>{setPreview(undefined);setOutcomes([]);setSelected(current=>e.target.checked?[...current,close.id]:current.filter(id=>id!==close.id));}} /> {close.label}</label>)}
    <button type="button" disabled={busy || selected.length===0} onClick={()=>void inspect()}>{busy?"Checking…":`Preview ${selected.length} selected close${selected.length===1?"":"s"}`}</button>
    {preview && <div><h3>Exact {mode} preview</h3><ul>{preview.map(close=><li key={close.id}>{close.label}: {close.count} {mode === "prepare" ? "source" : "unresolved"} job{close.count===1?"":"s"}</li>)}</ul>{mode === "stop" && <label>Reason for stopping<textarea value={reason} onChange={e=>setReason(e.target.value)} maxLength={500} /></label>}<button type="button" disabled={busy || (mode === "stop" && !reason.trim()) || preview.every(close=>close.count===0)} onClick={()=>void submit()}>{mode === "prepare" ? "Coordinate selected closes" : "Stop listed work"}</button></div>}
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
        ...(kind==="accrual" ? {reverse_next_month:data.get("reverse_next_month")==="true"} : Object.fromEntries(["credit_amount","credit_period","credit_reference","booked_balance","booking_reference","cancellation_method","cancellation_period","cancellation_reference"].map(key=>[key,String(data.get(key)??"")]))),
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
          {item.proposal.engine !== "openai" && (
            <p>
              AI interpretation is not configured. Only exact labeled lines are
              proposed; these are unverified candidates, not conclusions. An
              unlabeled invoice needs manual entry. Conflicting values remain
              blank.
            </p>
          )}
          {item.proposal.engine !== "openai" && (
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
          {kind !== "unsupported" && (
            <form onSubmit={confirm}>
              <fieldset disabled={busy}>
                <legend>
                  {kind === "reconciliation"
                    ? "Confirm proposed statement reconciliation"
                    : kind === "accrual" ? "Confirm proposed accrual" : "Confirm proposed prepaid preparation"}
                </legend>
                {facts.filter(f=>kind!=="prepaid" || !f.optional).map((f) => (
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
                {kind==="reconciliation" ? <label><input name="method" type="checkbox" required /> Confirm USD, complete page inventory, exact-date/signed-amount matching; ambiguous transactions stay unresolved.</label> : kind==="accrual" ? <AccrualConfirmations /> : <>
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
