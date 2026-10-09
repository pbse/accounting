import { useDates } from "../../data/time";
import { workPath, workspaceQuery } from "../../data/queries";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { PrepaidMethodFields, readCredits } from "../prepaids/MethodFields";
import { AccrualConfirmations } from "../accruals/Prepare";
import { EmailHistory, PasscodeNotice, SendRequestEmail, type EmailRecord, type ReminderStatus } from "./Email";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { Segmented } from "@close/ui/components/Segmented";
import { SourceReview, type Source } from "./Evidence";
import { PlanChips, type Plan } from "./Planning";
import { workspaceRequest } from "./workspace";
import { EntityCell } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";

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
  agent_checkpoint: { stage?: string; input_tokens?: number; output_tokens?: number; straight_through?: string };
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
    entity_name?: string;
    summary?: string;
    questions?: string[];
    unselected?: Source[];
    lines?: { line: number; row: string; facts: Fact[]; set_aside?: string }[];
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
    reminder?: ReminderStatus;
    link?: {
      expires_at: string;
      revoked_at: string | null;
      used_at: string | null;
    } | null;
    answers?: { position: number; state: string }[];
    partial?: boolean;
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
export const kindLabels: Record<string, string> = { prepaid: "Prepaid schedule", accrual: "Accrued expense", reconciliation: "Statement reconciliation", settlement: "Stripe settlement", deferred_revenue: "Deferred revenue", depreciation: "Depreciation", loan: "Loan schedule", owner: "Owner item", payroll: "Payroll" };

const formFor: Record<string, [string, string]> = {
  deferred_revenue: ["prepaids/new", "schedule=deferred_revenue&"],
  depreciation: ["prepaids/new", "schedule=depreciation&"],
  loan: ["loans/new", ""],
  owner: ["owners/new", ""],
  payroll: ["payroll/new", ""],
};


// The preparation inbox: on a close's Overview, that close's documents; on its own page (/dashboard/work, which the
// Home card "Prepared by the agent" opens), every document across the reader's clients, filtered by client or month.
export function Work({ closeId, onPrepared }: { closeId?: string; onPrepared?: () => void }) {
  const dates = useDates();
  const [refresh, setRefresh] = useState(0);
  // A link to one document of this close shows only that document, open; a link to the Preparation page may name
  // its view (Home's card opens every job, prepared ones included).
  const [params] = useSearchParams();
  const [offset, setOffset] = useState(0);
  const linkedView = closeId ? "" : views.some(([key]) => key === params.get("view")) ? params.get("view")! : "";
  const [filter, setFilter] = useState(closeId ? "all" : linkedView || "active");
  // A later link naming another view (the agent's citation of blocked work) changes the view on the page already open.
  useEffect(() => {
    if (linkedView) {
      setFilter(linkedView);
      setOffset(0);
    }
  }, [linkedView]);
  const [client, setClient] = useState("");
  const job = closeId ? (params.get("job") ?? "") : "";
  const opened = useRef("");
  const failures = useRef({ count: 0, seen: 0 });
  // Each view, client, page and linked job is its own read: with the page (its loader), then every three seconds, and
  // after a failure less often, up to every 30 seconds. A new view keeps the last rows on screen until it answers.
  const read = useQuery({
    ...workspaceQuery<{ items: WorkItem[]; counts?: Record<string, number>; has_more?: boolean }>(workPath(closeId, filter, client, offset, job)),
    placeholderData: keepPreviousData,
    refetchInterval: (query) => {
      // Consecutive failed reads, counted here: the query's own count starts again with every read.
      // Only a read that answered ends the run of failures; a read under way after a failure does not.
      const counted = failures.current;
      if (query.state.errorUpdatedAt > counted.seen) {
        counted.seen = query.state.errorUpdatedAt;
        counted.count += 1;
      }
      if (query.state.dataUpdatedAt > query.state.errorUpdatedAt) counted.count = 0;
      return counted.count ? Math.min(30_000, 3000 * 2 ** counted.count) : 3000;
    },
  });
  const items = read.data?.items ?? [];
  const counts = read.data?.counts ?? {};
  const more = !!read.data?.has_more;
  const loaded = read.data !== undefined;
  const error = read.error ? read.error.message || "Work is unavailable." : "";
  useEffect(() => {
    if (!job) {
      opened.current = "";
      return;
    }
    const target = document.getElementById(`work-${job}`);
    if (opened.current !== job && target instanceof HTMLDetailsElement) {
      opened.current = job;
      target.open = true;
      target.scrollIntoView({ block: "start" });
    }
  }, [job, items]);
  // While a new view loads the previous rows stay, without an update time of their own.
  const updated = loaded && !error && !read.isPlaceholderData && <small>Updated {dates.time(read.dataUpdatedAt)}</small>;
  return (
    <section id={closeId ? "close-work" : undefined} className={closeId ? undefined : "bare"} aria-label={closeId ? "Work from this close's evidence" : "Preparation across your clients"}>
      {closeId ? (
        <SectionHeader title="Work from your evidence">{updated}</SectionHeader>
      ) : (
        <PageHeader
          icon="work"
          title="Preparation"
          actions={
            <form
              className="title-actions"
              role="search"
              onSubmit={(e) => {
                e.preventDefault();
                setClient(String(new FormData(e.currentTarget).get("client") ?? "").trim());
                setOffset(0);
              }}
            >
              <input name="client" aria-label="Filter by client or close month" placeholder="Client or YYYY-MM" maxLength={200} />
              <button>Filter</button>
            </form>
          }
        >
          Every document the agent is reading, waiting on or has prepared across your clients. {updated}
        </PageHeader>
      )}
      {error && <p role="alert">{error} Retrying automatically.</p>}
      {!loaded && !error && <p role="status">Loading work…</p>}
      {loaded && (
        <>
          <div className="toolbar" id="work-list">
            <Segmented
              label="Work view"
              items={views.map(([key, label]) => ({ key, label, count: counts[key], alert: key === "needs_confirmation" && counts[key] > 0 }))}
              value={filter}
              onChange={(key) => {
                setFilter(key);
                setOffset(0);
              }}
            />
          </div>
          {job && (
            <p className="assistant-note">
              Showing the document you opened. <Link to={`/dashboard/closes/${closeId}`}>Show all work</Link>
            </p>
          )}
          {items.length === 0 && <p className="empty">{closeId ? "No work matches this view. Upload evidence to this close to begin." : "No work matches this view."}</p>}
          <fieldset disabled={!!error} className="row-list">
            {items.map((item) => (
              <details key={item.id} id={`work-${item.id}`} className="work-item">
                <summary className="row">
                  <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`${item.source_name} · #${item.id}`} />
                  <span className={item.stale || item.blockers.length > 0 ? "status danger" : (tones[item.state] ?? "status")}>
                    {item.stale ? "Source replaced" : item.blockers.length > 0 ? "Prepared · review blockers" : (labels[item.state] ?? item.state)}
                  </span>
                  <span>
                    <small>{item.proposal.kind && item.proposal.kind !== "unsupported" ? (kindLabels[item.proposal.kind] ?? item.proposal.kind) : ""}</small> <PlanChips plan={item} />
                  </span>
                  <span className="chip">Open</span>
                </summary>
                <div className="work-body">
                  <WorkDetails refresh={refresh} key={`${item.id}:${item.revision}`} item={item} onPrepared={onPrepared} onChange={() => (setRefresh((n) => n + 1), void read.refetch())} />
                </div>
              </details>
            ))}
          </fieldset>
          {closeId ? (
            more && <p className="note">Showing the first 100 jobs, with decisions first.</p>
          ) : (
            (offset > 0 || more) && (
              <p className="toolbar">
                <button type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 100))}>
                  Newer
                </button>
                <button type="button" disabled={!more} onClick={() => setOffset(offset + 100)}>
                  Older
                </button>
                <small>Jobs {offset + 1}–{offset + items.length}, decisions first</small>
              </p>
            )
          )}
        </>
      )}
    </section>
  );
}

type Hint = {
  field: string;
  proposed: string;
  in_ledger: boolean | null;
  procedure: string;
  procedure_usable: boolean;
  usual: { value: string; count: number; last_period: string; usable: boolean }[];
};

// What this client's accountant approved before, and whether QuickBooks has the proposed account. Nothing is filled in by itself.
function MemoryHint({ hint }: { hint: Hint }) {
  const use = (value: string) => (event: React.MouseEvent<HTMLButtonElement>) => {
    const input = event.currentTarget.form?.elements.namedItem(hint.field);
    if (input instanceof HTMLInputElement) input.value = value;
  };
  const same = (value: string) => value.trim().toLowerCase() === hint.proposed.trim().toLowerCase();
  // Matching the client's most common choice needs no note; otherwise the other choices are worth seeing.
  const usual = hint.usual.length > 0 && same(hint.usual[0].value) ? [] : hint.usual.filter((u) => !same(u.value));
  if (hint.in_ledger !== false && !(hint.procedure && !same(hint.procedure)) && usual.length === 0) return null;
  return (
    <p className="source-label">
      {hint.in_ledger === false && <>Not in this QuickBooks snapshot. </>}
      {hint.procedure && !same(hint.procedure) && (
        <>
          Your approved procedure names {hint.procedure}.{" "}
          {hint.procedure_usable && (
            <button type="button" onClick={use(hint.procedure)}>
              Use {hint.procedure}
            </button>
          )}{" "}
        </>
      )}
      {usual.length > 0 && (
        <>
          Approved before for this client:{" "}
          {usual.map((u) => (
            <span key={u.value}>
              {u.value} ({u.count}×, last {u.last_period}){" "}
              {u.usable && (
                <button type="button" onClick={use(u.value)}>
                  Use
                </button>
              )}{" "}
            </span>
          ))}
        </>
      )}
    </p>
  );
}

// A prior accountant's schedule, read on the client's opening month, is decided whole: each row imported as a schedule
// (no journal) with a method, or set aside with a reason.
function ScheduleImport({ item, lines, busy, blocked, onImport }: { item: WorkItem; lines: NonNullable<WorkItem["proposal"]["lines"]>; busy: boolean; blocked: boolean; onImport: (body: object) => void }) {
  if (item.proposal.kind === "loan") return <LoanScheduleImport item={item} lines={lines} busy={busy} blocked={blocked} onImport={onImport} />;
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    onImport({
      client_confirmed: data.has("client"),
      roster_confirmed: data.has("roster"),
      inventory_confirmed: data.has("inventory"),
      lines: lines.map((l) => {
        const choice = String(data.get(`decision-${l.line}`));
        return choice === "set_aside" ? { line: l.line, decision: "set_aside", reason: String(data.get(`reason-${l.line}`) ?? "").trim() } : { line: l.line, decision: "import", method: choice };
      }),
    });
  };
  return (
    <form onSubmit={submit}>
      <p>
        {lines.length} row{lines.length === 1 ? "" : "s"} read from {item.source_name}. Each row you import becomes a schedule as of this month-end, with no journal; it is
        refused if the calculation does not give the balance the schedule states. The opening baseline then ties the schedules to the Trial Balance.
      </p>
      <fieldset disabled={busy || blocked}>
        {lines.map((l) => {
          const value = (field: string) => l.facts.find((f) => f.field === field)?.value ?? "";
          const open = l.facts.filter((f) => f.question);
          return (
            <fieldset key={l.line}>
              <legend>
                Row {l.line}: {value("label") || "unlabeled"} · {value("amount")} {value("currency")} · {value("service_start")} to {value("service_end")} · {item.proposal.kind === "depreciation" ? "net book value" : "remaining"} {value("remaining")}{value("salvage_value") ? ` · salvage ${value("salvage_value")}` : ""}
              </legend>
              <small>{l.row}</small>
              {open.length > 0 && (
                <ul>
                  {open.map((f) => (
                    <li key={f.field}>{f.question}</li>
                  ))}
                </ul>
              )}
              <label>
                Decision
                <select name={`decision-${l.line}`} defaultValue={open.length > 0 ? "set_aside" : "equal_full_months_v1"}>
                  <option value="equal_full_months_v1" disabled={open.length > 0}>Import · equal full months</option>
                  <option value="actual_days_v1" disabled={open.length > 0}>Import · actual days</option>
                  <option value="set_aside">Set aside</option>
                </select>
              </label>
              <label>
                Why it is set aside (only when setting it aside)
                <input name={`reason-${l.line}`} maxLength={2000} />
              </label>
            </fieldset>
          );
        })}
        <label>
          <input type="checkbox" name="client" required /> This schedule is {item.entity}'s.
        </label>
        <label>
          <input type="checkbox" name="roster" required /> These {lines.length} rows are all of the schedule's rows.
        </label>
        {(item.proposal.unselected?.length ?? 0) > 0 && (
          <label>
            <input type="checkbox" name="inventory" required /> I reviewed the evidence the agent did not use.
          </label>
        )}
        <button className="primary">Import schedule</button>
      </fieldset>
      {blocked && <p>Answer the questions above first; the agent reads the schedule again.</p>}
    </form>
  );
}

// A prior loan's amortization schedule is imported whole as one schedule-only loan draft for this month.
function LoanScheduleImport({ item, lines, busy, blocked, onImport }: { item: WorkItem; lines: NonNullable<WorkItem["proposal"]["lines"]>; busy: boolean; blocked: boolean; onImport: (body: object) => void }) {
  const loan = item.proposal.facts ?? [];
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    onImport({
      client_confirmed: data.has("client"),
      roster_confirmed: data.has("roster"),
      inventory_confirmed: data.has("inventory"),
      day_count: String(data.get("day_count")),
      reclass_current_portion: data.has("reclass"),
      current_portion_account: String(data.get("current_account") ?? "").trim(),
    });
  };
  return (
    <form onSubmit={submit}>
      <p>
        {loan.map((f) => `${f.label}: ${f.value || f.question}`).join(" · ")}. The payment before this month opens it, this month's payments close it, and every row must be the balance
        before it less its principal. It becomes a schedule-only loan draft with no journal; the opening baseline ties it to the Trial Balance.
      </p>
      <table>
        <caption>{lines.length} payment rows read from {item.source_name}</caption>
        <thead>
          <tr>
            <th>Date</th>
            <th>Interest</th>
            <th>Principal</th>
            <th>Balance</th>
          </tr>
        </thead>
        <tbody>
          {lines.map((l) => (
            <tr key={l.line}>
              {["date", "interest", "principal", "balance"].map((field) => {
                const fact = l.facts.find((f) => f.field === field);
                return <td key={field}>{fact?.value || fact?.question}</td>;
              })}
            </tr>
          ))}
        </tbody>
      </table>
      <fieldset disabled={busy || blocked}>
        <label>
          Day-count the note states
          <select name="day_count" defaultValue="actual_365">
            <option value="actual_360">Actual/360</option>
            <option value="actual_365">Actual/365 (fixed)</option>
            <option value="30_360_bond">30/360 bond basis</option>
            <option value="30e_360">30E/360</option>
          </select>
        </label>
        <label>
          <input type="checkbox" name="reclass" /> Show the principal due in the 12 months after month-end as a current liability
        </label>
        <label>
          Current portion account (only with the current liability)
          <input name="current_account" maxLength={200} placeholder="Loan, current portion" />
        </label>
        <label>
          <input type="checkbox" name="client" required /> This loan is {item.entity}'s.
        </label>
        <label>
          <input type="checkbox" name="roster" required /> These rows include the last payment before this month, every payment this month and, for a current portion, every payment of the 12 months after it.
        </label>
        {(item.proposal.unselected?.length ?? 0) > 0 && (
          <label>
            <input type="checkbox" name="inventory" required /> I reviewed the evidence the agent did not use.
          </label>
        )}
        <button className="primary">Import loan schedule</button>
      </fieldset>
      {blocked && <p>Answer the questions above first; the agent reads the schedule again.</p>}
    </form>
  );
}

function WorkDetails({
  item,
  onChange,
  onPrepared,
  refresh = 0,
}: {
  item: WorkItem;
  onChange: () => void;
  onPrepared?: () => void;
  refresh?: number;
}) {
  const dates = useDates();
  const [busy, setBusy] = useState(false);
  const [replyLink, setReplyLink] = useState("");
  const [passcode, setPasscode] = useState("");
  const [error, setError] = useState("");
  const facts = item.proposal.facts ?? [];
  const kind = item.proposal.kind ?? "prepaid";
  const questions = item.proposal.questions ?? [];
  const lines = item.proposal.lines ?? [];
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
        ...(item.proposal.lines ?? []).flatMap((l) => l.facts.filter((f) => f.question).map((f) => `Row ${l.line}: ${f.question}`)),
      ].map((q, i) => `${i + 1}. ${q}`).join("\n") ||
      "Please clarify the following specific missing information:"
    }`,
  );
  const path = `/${item.close_id}/work/${item.id}`;
  const [hints, setHints] = useState<Record<string, Hint>>({});
  useEffect(() => {
    setHints({});
    if (item.state !== "needs_confirmation") return;
    let live = true;
    workspaceRequest(`${path}/hints`)
      .then((data: { fields: Hint[] }) => live && setHints(Object.fromEntries(data.fields.map((h) => [h.field, h]))))
      .catch(() => live && setHints({}));
    return () => {
      live = false;
    };
  }, [path, item.state, refresh]);
  async function act(action: string, body: object = {}) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`${path}/${action}`, {
        revision: item.revision,
        ...body,
      });
      if (result.link_token) {
        setReplyLink(`${window.location.origin}/request#${result.link_token}`);
        setPasscode(result.passcode ?? "");
      }
      if (action === "revoke_link") {
        setReplyLink("");
        setPasscode("");
      }
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
      facts.filter((f) => !["vendor", "billed_to", "invoice_number"].includes(f.field)).map((f) => [f.field, String(data.get(f.field) ?? "")]),
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
      {item.agent_checkpoint?.straight_through && <p>{item.agent_checkpoint.straight_through}</p>}
      {item.coordinated && item.agent_attempts >= 3 && <p role="status">The agent has read this document three times without settling it; a client answer or a recheck lets it read again.</p>}
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
          work. Started {dates.dateTime(item.agent_started_at)}. No
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
            {["openai", "context_machine"].includes(item.proposal.engine ?? "")
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
          {item.proposal.engine === "identity_blocked" && (item.proposal.entity_name ? (
            <p role="alert">
              This evidence names the client “{item.proposal.entity_name}”. If that is {item.entity}, confirm it once and the work is rechecked; future documents using that name are accepted. Otherwise replace the misfiled source.{" "}
              <button disabled={busy} onClick={() => void act("alias", { alias: item.proposal.entity_name })}>Same client as {item.entity}</button>
            </p>
          ) : <p role="alert">This evidence names a different or ambiguous client. Compare it with the original and replace the misfiled source before preparing this work.</p>)}
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
          {["openai", "context_machine"].includes(item.proposal.engine ?? "") && (
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
          {lines.length > 0 && <ScheduleImport item={item} lines={lines} busy={busy} blocked={questions.length > 0} onImport={(body) => void act("import_schedule", body)} />}
          {formFor[kind] && lines.length === 0 && (
            <p>
              {facts.filter((f) => f.candidates.length > 0).map((f) => (
                <span key={f.field} className="block">
                  {f.label}: {f.value || f.question} · source #{f.candidates[0].source_id}, {f.candidates[0].location}
                </span>
              ))}
              {questions.length > 0 ? <span>Answer the questions above; the form opens once the agent has none left.</span> : <Link
                className="button primary"
                to={`/dashboard/${formFor[kind][0]}?${formFor[kind][1]}close=${item.close_id}&entity=${encodeURIComponent(item.entity)}&period=${item.period}&work=${item.id}&revision=${item.revision}`}
              >
                Open the {kindLabels[kind].toLowerCase()} form with these facts →
              </Link>}
            </p>
          )}
          {kind !== "unsupported" && kind !== "settlement" && !formFor[kind] && item.proposal.engine !== "recognized_export" && lines.length === 0 && (
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
                    {hints[f.field] && <MemoryHint hint={hints[f.field]} />}
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
                {kind==="reconciliation" ? <label><input name="method" type="checkbox" required /> Confirm the client's home currency, complete page inventory, exact-date/signed-amount matching; ambiguous transactions stay unresolved.</label> : kind==="accrual" ? <>{facts.filter(f=>f.optional && f.candidates.length>0).map(f=><p key={f.field}>{f.label}: {f.candidates.map(c=>`${c.value} · evidence #${c.source_id} ${c.location}: ${c.quote}`).join("; ")}</p>)}<AccrualConfirmations proRata={false} /></> : <>
                  {facts.filter(f=>f.optional && f.candidates.length>0).map(f=><p key={f.field}>{f.label}: {f.candidates.map(c=>`${c.value} · evidence #${c.source_id} ${c.location}: ${c.quote}`).join("; ")}{f.question}</p>)}
                  <PrepaidMethodFields values={Object.fromEntries(facts.map(f=>[f.field,f.value]))} />
                </>}
                <button
                  className="primary"
                  disabled={
                    ["openai", "context_machine"].includes(item.proposal.engine ?? "") && questions.length > 0
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
                      : q.id === latest.id
                        ? "Approved · not sent"
                        : `Replaced by request #${latest.id} · never sent`}
              </summary>
              <p>
                To: {q.recipient} · approved by {q.approved_by}
              </p>
              <pre>{q.body}</pre>
              {!!q.emails?.length && <EmailHistory emails={q.emails} closeId={item.close_id} />}
              {q.delivery_reference && (
                <p>Delivery record: {q.delivery_reference}</p>
              )}
            </details>
          ))}
        </section>
      )}
      {item.state !== "waiting_client" && latest && !latest.consolidated && latest.link && !latest.link.revoked_at && <p>This request's reply link is still active. <button disabled={busy} onClick={() => void act("revoke_link", { request_id: latest.id })}>Revoke reply link</button></p>}
      {item.state === "waiting_client" && latest?.consolidated && <p>Manage this consolidated request in <a href={`/dashboard/closes/${item.close_id}#coverage-work`}>Coverage and client evidence</a>.</p>}
      {item.state === "waiting_client" && latest && !latest.consolidated && !item.stale && (
        <fieldset disabled={busy}>
          {(!latest.email || (latest.email.state === "accepted" && latest.email.delivery === "delivered") || (latest.reminder?.left ?? 1) <= 0) && !latest.link?.used_at && !latest.link?.revoked_at && (
            <SendRequestEmail
              key={latest.email?.id ?? "initial"}
              previousId={latest.email?.id}
              reminder={latest.reminder}
              closeId={item.close_id}
              jobId={item.id}
              requestId={latest.id}
              revision={item.revision}
              recipient={latest.recipient}
              onQueued={(p) => {
                if (!latest.email) {
                  setPasscode(p);
                  setReplyLink("");
                }
                onChange();
              }}
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
                    ? latest.partial
                      ? `Partial reply: ${latest.answers?.filter((a) => a.state === "answered").length ?? 0} answered, ${latest.answers?.filter((a) => a.state === "awaiting_document").length ?? 0} awaiting a document. The client can add to it until the link expires; work waits for you or for a complete reply.`
                      : "Reply received. The client can still add documents or correct it until the link expires."
                    : `Last link expires ${dates.dateTime(latest.link.expires_at)}.`}
              </p>
            )}
            {latest.partial && item.state === "waiting_client" && (
              <button onClick={() => void act("resume")}>Resume now with the partial reply</button>
            )}
            <button onClick={() => void act("link", { request_id: latest.id })}>
              Create new private reply link
            </button>
            {latest.link && !latest.link.revoked_at && (
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
            <PasscodeNotice passcode={passcode} />
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
                Reply as received
                <textarea name="body" rows={6} required maxLength={20000} />
              </label>
              <p>
                Preserved as evidence, with card numbers, IBANs, Social Security numbers, labeled bank account numbers and passwords removed. Candidate facts return for review on this
                same job; a reply cannot approve assumptions or posting.
              </p>
              <button>Record reply and resume this job</button>
            </form>
          )}
        </fieldset>
      )}
      {item.replies.length > 0 && (
        <section>
          <h3>Replies · review required</h3>
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
