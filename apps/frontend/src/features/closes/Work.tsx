import { PrepaidMethodFields, readCredits } from "../prepaids/MethodFields";
import { AccrualConfirmations } from "../accruals/Prepare";
import { EmailHistory, PasscodeNotice, SendRequestEmail, type EmailRecord, type ReminderStatus } from "./Email";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { AgentComposer } from "@close/ui/components/AgentComposer";
import { Avatar } from "@close/ui/components/Avatar";
import { Icon } from "@close/ui/components/Icon";
import { PageHeader } from "@close/ui/components/PageHeader";
import { Segmented } from "@close/ui/components/Segmented";
import { StatGroup, StatTile } from "@close/ui/components/StatTile";
import { SourceReview, type Source } from "./Evidence";
import { PlanChips, type Plan } from "./Planning";
import { workspaceRequest } from "./workspace";
import { MorningBrief, NotOpened, type Morning } from "./Morning";
import { DelegatedCloses } from "./Goal";
import { Setup } from "./Setup";
import { EntityCell, RowList } from "@close/ui/components/RowList";
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
type Dependency = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; upstream_workpaper_id: string };
type Predicted = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; transaction: string; account: string; updated_at: string; approved_at: string; count: number };
type Untied = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; account: string; balance: string; expected: string; difference: string; captured_at: string };
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
type QueueItem = Plan & { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; created_at: string; digest: string; review_id: string; bulk_reason: string; approval_note?: string; approval_requested_by?: string; approval_agent?: string };
const kindLabels: Record<string, string> = { prepaid: "Prepaid schedule", accrual: "Accrued expense", reconciliation: "Statement reconciliation", settlement: "Stripe settlement", deferred_revenue: "Deferred revenue", depreciation: "Depreciation", loan: "Loan schedule", owner: "Owner item", payroll: "Payroll" };

const formFor: Record<string, [string, string]> = {
  deferred_revenue: ["prepaids/new", "schedule=deferred_revenue&"],
  depreciation: ["prepaids/new", "schedule=depreciation&"],
  loan: ["loans/new", ""],
  owner: ["owners/new", ""],
  payroll: ["payroll/new", ""],
};

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
  const [predicted, setPredicted] = useState<{ items: Predicted[]; more: boolean }>({ items: [], more: false });
  const [untied, setUntied] = useState<{ items: Untied[]; more: boolean }>({ items: [], more: false });
  const [queue, setQueue] = useState<{ items: QueueItem[]; count: number }>();
  const [queueError, setQueueError] = useState("");
  const [asOf, setAsOf] = useState<Date>();
  const [sectionsAt, setSectionsAt] = useState<Date>();
  // Bumped every 30 seconds, on a changed job and on a recovered poll: every section but the job list reads again.
  const [cycle, setCycle] = useState(0);
  const seen = useRef("");
  const [morning, setMorning] = useState<Morning>();
  const [morningError, setMorningError] = useState("");
  // A link to one document of this close shows only that document, open.
  const [params] = useSearchParams();
  const job = closeId ? (params.get("job") ?? "") : "";
  const opened = useRef("");
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
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    let failures = 0;
    seen.current = "";
    async function load() {
      try {
        const data = await workspaceRequest(
          `${closeId ? `/${closeId}/work` : "/work"}?view=${filter}&client=${encodeURIComponent(clientSearch)}${job ? `&jobs=${encodeURIComponent(job)}` : ""}`,
        );
        if (!live) return;
        setItems(data.items);
        setCounts(data.counts ?? {});
        setMore(data.has_more);
        setError("");
        setLoaded(true);
        setAsOf(new Date());
        const state = JSON.stringify([data.items.map((i: WorkItem) => [i.id, i.state, i.revision]), data.counts]);
        if (!closeId && seen.current && (seen.current !== state || failures > 0)) setCycle((n) => n + 1);
        seen.current = state;
        failures = 0;
        timer = setTimeout(load, 3000);
      } catch (e) {
        if (!live) return;
        setError(e instanceof Error ? e.message : "Inbox unavailable.");
        failures++;
        timer = setTimeout(load, Math.min(30_000, 3000 * 2 ** failures));
      }
    }
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [closeId, refresh, filter, clientSearch, job]);
  useEffect(() => {
    if (closeId) return;
    const timer = setInterval(() => setCycle((n) => n + 1), 30_000);
    return () => clearInterval(timer);
  }, [closeId]);
  // A failed read keeps its section's last good result beside the error until a later read succeeds.
  useEffect(() => {
    if (closeId) return;
    let live = true;
    const dependencies = workspaceRequest(`/dependencies?client=${encodeURIComponent(clientSearch)}`).then(data => {
      if (live) { setDependencies(data.items); setDependencyMore(data.has_more); setPredicted({ items: data.predicted ?? [], more: data.predicted_more }); setUntied({ items: data.no_longer_ties ?? [], more: data.no_longer_ties_more }); setDependencyError(""); setDependencyLoaded(true); }
    }).catch(e => {
      if (live) { setDependencyError(e instanceof Error ? e.message : "Dependency review unavailable."); setDependencyLoaded(true); }
    });
    const queue = workspaceRequest(`/review-queue?client=${encodeURIComponent(clientSearch)}`).then(data => {
      if (live) { setQueue(data); setQueueError(""); }
    }).catch(e => live && setQueueError(e instanceof Error ? e.message : "The review queue is unavailable."));
    const brief = workspaceRequest(`/morning?client=${encodeURIComponent(clientSearch)}`)
      .then((data) => {
        if (live) { setMorning(data); setMorningError(""); }
      })
      .catch((e) => live && setMorningError(e instanceof Error ? e.message : "The morning brief is unavailable."));
    void Promise.all([dependencies, queue, brief]).then(() => live && setSectionsAt(new Date()));
    return () => { live = false; };
  }, [closeId, clientSearch, refresh, cycle]);
  const filterClients = (value: string) => {
    if (value === clientSearch) return setRefresh((n) => n + 1);
    setClientSearch(value);
    setDependencyLoaded(false);
    setDependencies([]);
    setDependencyMore(false);
    setPredicted({ items: [], more: false });
    setUntied({ items: [], more: false });
    setDependencyError("");
    setQueue(undefined);
    setQueueError("");
    setMorning(undefined);
    setMorningError("");
  };
  const dueCounts = morning?.due_counts;
  const visiblePredicted = predicted.items.filter(item => !untied.items.some(u => u.workpaper_id === item.workpaper_id));
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
          <PageHeader
            icon="work"
            title="Work"
            actions={
              <form
                className="title-actions"
                role="search"
                onSubmit={(e) => {
                  e.preventDefault();
                  filterClients(String(new FormData(e.currentTarget).get("client") ?? ""));
                }}
              >
                <input name="client" aria-label="Filter by client or close month" placeholder="Client or YYYY-MM" maxLength={200} />
                <button>Filter</button>
                <Link className="button primary" to="/dashboard/closes">
                  Open a client close
                </Link>
              </form>
            }
          >
            What needs you, what the agent is handling, and what can finish next.
          </PageHeader>
          <PortfolioCommands key={`${filter}:${clientSearch}`} items={items} view={filter} client={clientSearch} onFilter={show} onChange={() => setRefresh((n) => n + 1)} />
          <StatGroup>
            <StatTile icon="decision" tone="orange" label="Needs your decision" value={counts.needs_confirmation} caption="Proposed facts and questions to confirm" onClick={() => show("needs_confirmation")} />
            <StatTile icon="review" label="Ready for review" value={queue?.count} caption="Prepared drafts with evidence and no blockers" onClick={() => document.getElementById("review-queue")?.scrollIntoView({ block: "start" })} />
            <StatTile icon="waiting" tone="teal" label="Waiting on clients" value={counts.waiting_client} caption="Approved requests awaiting a reply" onClick={() => show("waiting_client")} />
            <StatTile
              icon="accrual"
              tone={dueCounts?.overdue ? "red" : "gray"}
              label="Due in 7 days"
              value={dueCounts && dueCounts.overdue + dueCounts.upcoming}
              caption={dueCounts ? `${dueCounts.overdue} overdue · ${dueCounts.upcoming} upcoming · ${dueCounts.no_due_date} without a due date` : "Closes not yet signed off, or changed since sign-off"}
              onClick={() => document.getElementById("due-closes")?.scrollIntoView({ block: "start" })}
            />
            <StatTile icon="blocked" tone="red" label="Blocked" value={counts.blocked} caption="Failed, replaced or unsupported work" onClick={() => show("blocked")} />
          </StatGroup>
        </>
      )}
      {!closeId && <Setup onEnrolled={() => setRefresh((n) => n + 1)} />}
      {error && (
        <p role="alert">{error} Retrying automatically{asOf ? `; last updated ${asOf.toLocaleTimeString()}` : ""}.</p>
      )}
      {asOf && !error && (
        <p className="note">
          Jobs updated {asOf.toLocaleTimeString()}
          {!closeId && sectionsAt && `; the brief, review queue and dependencies ${sectionsAt.toLocaleTimeString()}, read again every 30 seconds and whenever work changes`}.
        </p>
      )}
      {!loaded && !error && <p role="status">Loading work…</p>}
      {!closeId && morning && morningError && <p role="alert">{morningError} Showing the last brief read.</p>}
      {!closeId && <MorningBrief brief={morning} error={morningError} />}
      {!closeId && morning && (
        <>
          <SectionHeader title="Due in 7 days" id="due-closes">
            <small>Overdue or due within a week and not signed off, or changed since sign-off. {morning.due_counts.no_due_date > 0 && `${morning.due_counts.no_due_date} outstanding ${morning.due_counts.no_due_date === 1 ? "close has" : "closes have"} no due date.`}</small>
          </SectionHeader>
          <NotOpened months={morning.not_opened ?? []} />
          <RowList label="Due closes">
            {morning.due.length === 0 && <p className="empty">No outstanding close is overdue or due within 7 days.</p>}
            {morning.due.map((item) => (
              <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}`}>
                <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`Due ${item.due_on}`} />
                {item.due_on < morning.as_of.slice(0, 10) ? <span className="status danger">Overdue</span> : <span className="status warn">Due soon</span>}
                <small>{{ open: "Not signed off", reopen_required: "Changed since sign-off", unknown: "Sign-off could not be rechecked" }[item.state]}</small>
                <Icon name="arrow" />
              </Link>
            ))}
          </RowList>
        </>
      )}
      {loaded && (
        <>
          {!closeId && <DelegatedCloses key={clientSearch} refresh={refresh + cycle} client={clientSearch} />}
          {!closeId && (
            <>
              <SectionHeader title="Ready for your review" id="review-queue">
                {queue && queue.count > queue.items.length && <small>Showing {queue.items.length} of {queue.count}</small>}
              </SectionHeader>
              {queueError && <p role="alert">{queueError}{queue ? " Showing the last queue read." : ""}</p>}
              <BulkReview items={queue?.items} onDone={() => setRefresh((n) => n + 1)} />
              <SectionHeader title="Work needing re-review" id="dependency-review">
                <small>Source revisions and inherited schedules reopen review; earlier approvals are not reused.</small>
              </SectionHeader>
              <RowList label="Portfolio dependency review">
                {!dependencyLoaded && <p role="status" className="empty">Checking dependencies…</p>}
                {dependencyError && <p role="alert" className="empty">{dependencyError}</p>}
                {dependencyLoaded && !dependencyError && dependencies.length === 0 && <p className="empty">No current workpapers have a changed dependency in this client filter.</p>}
                {dependencies.map(item => (
                  <Link className="row" key={item.workpaper_id} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
                    <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`${kindLabels[item.kind] ?? item.kind} · draft #${item.workpaper_id}`} />
                    <span className="status danger">Evidence changed</span>
                    <small>{item.upstream_workpaper_id ? `Linked to prior draft #${item.upstream_workpaper_id}` : "Linked source changed"}</small>
                    <Icon name="arrow" />
                  </Link>
                ))}
              </RowList>
              {dependencyMore && <p>Showing 100 recent affected workpapers. Filter by client or month to see more.</p>}
              {(untied.items.length > 0 || untied.more) && (
                <RowList label="No longer ties">
                  <p><strong>No longer ties</strong> · approved drafts in open closes whose account differs from what the approved work supports in a Trial Balance captured after the approval. Sign-off is blocked until it ties; the approval stands until you review again.</p>
                  {untied.items.map(item => (
                    <Link className="row" key={`${item.workpaper_id}-${item.account}`} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
                      <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`${kindLabels[item.kind] ?? item.kind} · draft #${item.workpaper_id}`} />
                      <span className="status danger">No longer ties</span>
                      <small>{item.account} is {item.balance} in the Trial Balance captured {new Date(item.captured_at).toLocaleString()} but {item.expected} in its support, a difference of {item.difference}.</small>
                      <Icon name="arrow" />
                    </Link>
                  ))}
                  {untied.more && <p>Checked the 10 open closes with the newest Trial Balances. Filter by client or month to check others.</p>}
                </RowList>
              )}
              {visiblePredicted.length > 0 && (
                <RowList label="Predicted re-review">
                  <p><strong>May need re-review</strong> · approved drafts in open closes whose account had a transaction updated in QuickBooks after the approval. The update may not change the balance; the approval stands until you review again.</p>
                  {visiblePredicted.map(item => (
                    <Link className="row" key={item.workpaper_id} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
                      <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`${kindLabels[item.kind] ?? item.kind} · draft #${item.workpaper_id}`} />
                      <span className="status">May be affected</span>
                      <small>{item.transaction} on {item.account} updated {new Date(item.updated_at).toLocaleString()}, after approval on {new Date(item.approved_at).toLocaleString()}{item.count > 1 ? ` (${item.count} such transactions)` : ""}</small>
                      <Icon name="arrow" />
                    </Link>
                  ))}
                  {predicted.more && <p>Showing 100 drafts. Filter by client or month to see more.</p>}
                </RowList>
              )}
              <DailyBooks refresh={refresh + cycle} onReviewed={() => setRefresh((n) => n + 1)} />
            </>
          )}
          <div className="toolbar" id="work-list">
            <Segmented
              label="Work view"
              items={views.map(([key, label]) => ({ key, label, count: counts[key], alert: key === "needs_confirmation" && counts[key] > 0 }))}
              value={filter}
              onChange={setFilter}
            />
            <button type="button" onClick={() => setRefresh((n) => n + 1)}>Refresh</button>
          </div>
          {job && (
            <p className="assistant-note">
              Showing the document you opened. <Link to={`/dashboard/closes/${closeId}`}>Show all work</Link>
            </p>
          )}
          {items.length === 0 && (
            <p className="empty">
              No work matches this view. Change the filters or upload evidence
              in a client close.
            </p>
          )}
          <fieldset disabled={!!error} className="row-list">
            {items.map((item) => (
              <details
                key={item.id}
                id={`work-${item.id}`}
                className="work-item"
              >
                <summary className="row">
                  <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`${item.source_name} · #${item.id}`} />
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
                    refresh={refresh}
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

type Daily = { close_id: string; entity: string; period: string; source: "quickbooks" | "xero"; sync_id: string; captured_at: string; requested_by: string; total: number; new: { type: string; question: string }[]; unchecked?: string };

// New exceptions from this and last month's captures since each client was last reviewed.
function DailyBooks({ refresh, onReviewed }: { refresh: number; onReviewed: () => void }) {
  const [items, setItems] = useState<Daily[]>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest("/daily")
      .then((data) => {
        if (live) { setItems(data.items); setError(""); }
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Daily books are unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  async function reviewed(item: Daily) {
    setBusy(item.close_id);
    try {
      await workspaceRequest(`/${item.close_id}/daily-review`, { sync_id: item.sync_id, source: item.source });
      onReviewed();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The review could not be saved.");
    } finally {
      setBusy("");
    }
  }
  const open = items?.filter((item) => item.new.length > 0) ?? [];
  const unchecked = items?.filter((item) => item.unchecked) ?? [];
  return (
    <>
      <SectionHeader title="Daily books" id="daily-books">
        <small>New since each client's last review. QuickBooks and Xero captures refresh daily, and QuickBooks sandbox companies also after a change.</small>
      </SectionHeader>
      <RowList label="Daily books">
        {!items && !error && <p role="status" className="empty">Checking captures…</p>}
        {error && <p role="alert" className="empty">{error}</p>}
        {items && open.length === 0 && unchecked.length === 0 && <p className="empty">No new exceptions since the last review.</p>}
        {unchecked.map((item) => (
          <div className="row" key={`unchecked-${item.close_id}`}>
            <EntityCell
              name={item.entity}
              title={`${item.entity} · ${item.period}`}
              detail={`Transaction check incomplete in the ${item.source === "xero" ? "Xero" : "QuickBooks"} capture of ${new Date(item.captured_at).toLocaleString()}: ${item.unchecked}`}
            />
            <Link to={`/dashboard/closes/${item.close_id}?tab=overview`}>Open the close</Link>
          </div>
        ))}
        {open.map((item) => (
          <div className="row" key={item.close_id}>
            <EntityCell
              name={item.entity}
              title={`${item.entity} · ${item.period}`}
              detail={`${item.new.length} new of ${item.total} · captured ${new Date(item.captured_at).toLocaleString()}${item.requested_by === "quickbooks-change-hint" ? " after a QuickBooks change" : ""}`}
            />
            <small>{item.new.slice(0, 2).map((e) => e.question).join(" ")}</small>
            <Link to={`/dashboard/closes/${item.close_id}?tab=overview`}>Open exceptions</Link>
            <button type="button" disabled={busy === item.close_id} onClick={() => void reviewed(item)}>
              Mark reviewed
            </button>
          </div>
        ))}
      </RowList>
    </>
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
      <RowList>
        {!items && <p className="empty">Review queue unavailable. Open a close to review its drafts.</p>}
        {items && items.length === 0 && <p className="empty">Nothing is waiting for review in this filter.</p>}
        {items?.map((item) => (
          <div className="row" key={item.workpaper_id}>
            <EntityCell
              leading={
                <>
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
                </>
              }
              title={`${item.entity} · ${item.period}`}
              detail={`${kindLabels[item.kind] ?? item.kind} · draft #${item.workpaper_id}${item.bulk_reason ? ` · review individually: ${item.bulk_reason.toLowerCase()}` : ""}`}
            >
              {item.approval_note && <small title={item.approval_note}>Approval requested{item.approval_requested_by ? ` by ${item.approval_requested_by}` : ""}{item.approval_agent ? ` via ${item.approval_agent}` : ""}: {item.approval_note}</small>}
            </EntityCell>
            <span className="status warn">{item.approval_note ? "Approval requested" : "Awaiting review"}</span>
            <PlanChips plan={item} />
            <Link className="button" to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
              Review
            </Link>
          </div>
        ))}
      </RowList>
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

type Goal = { id: string; action: "prepare" | "recheck" | "stop" | "delegate" | "show"; finish?: "review" | "signoff"; goal: string; scope: string; has_more: boolean; reason: string; items: { close_id: string; label: string; count: number; state: string; result: string; due_on?: string }[] };

// A goal's exact server-selected closes. Each run acts on at most ten, each checked again against its preview.
function PortfolioGoal({ goal, onChange }: { goal: Goal; onChange: () => void }) {
  const [current, setCurrent] = useState(goal);
  // A listing asked again comes back as a new goal with the same key.
  useEffect(() => setCurrent(goal), [goal]);
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = current.items.filter((i) => i.state === "pending").length;
  const needsReason = current.action === "stop" && !current.reason;
  const noun = current.action === "prepare" || current.action === "delegate" ? "source document" : "job";
  const handing = current.action === "delegate";
  // A hand-over shows each close's deadline for the agent: its own due date when the close page would accept it.
  const pendingText = (i: Goal["items"][number]) =>
    handing
      ? `${i.count} ${noun}${i.count === 1 ? "" : "s"}; hand over ${i.due_on ? `with the deadline ${new Date(`${i.due_on}T00:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })}` : "with no deadline"}`
      : `${i.count} ${noun}${i.count === 1 ? "" : "s"} to ${current.action}`;
  async function run() {
    setBusy(true);
    setError("");
    try {
      let left = pending;
      while (left > 0) {
        const next: Goal = await workspaceRequest("/portfolio/run", { id: current.id, confirmed: true, reason: reason.trim() });
        setCurrent(next);
        const now = next.items.filter((i) => i.state === "pending").length;
        if (now >= left) break;
        left = now;
      }
      onChange();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The goal could not run.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label={`Goal: ${current.goal}`}>
      <h3>{current.goal}</h3>
      <p>
        {current.scope}.{current.has_more && " More than 100 closes matched; this goal covers the first 100 by due date."}
      </p>
      {current.items.length === 0 ? <p>No closes match this goal.</p> : (
        <ul>
          {current.items.map((i, n) => (
            <li key={i.close_id || n}>
              {i.close_id ? <Link to={`/dashboard/closes/${i.close_id}`}>{i.label}</Link> : i.label}: {i.state === "pending" ? pendingText(i) : i.result || i.state}
            </li>
          ))}
        </ul>
      )}
      {pending > 0 && (
        <>
          {needsReason && <label>Reason for stopping<textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} /></label>}
          <label><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> {handing
            ? `Hand the ${pending} pending close${pending === 1 ? "" : "s"} to the agent to get each ${current.finish === "signoff" ? "signed off" : "ready for review"}. A prepaid invoice whose facts the invoice and the QuickBooks capture both prove is drafted under the firm's procedure without field-by-field confirmation; everything else waits for a person. The agent never approves, sends, posts or signs off.`
            : `${current.action[0].toUpperCase() + current.action.slice(1)} the ${pending} pending close${pending === 1 ? "" : "s"}; facts and methods still need separate review.`}</label>
          <button type="button" disabled={busy || !confirmed || (needsReason && !reason.trim())} onClick={() => void run()}>{busy ? "Running…" : `Run for ${pending} close${pending === 1 ? "" : "s"}`}</button>
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}

// One-click asks: the first three only filter this page; preparing shows the server's exact preview first.
const suggestions = ["Show decisions", "Show blockers", "Show everything waiting on clients", "Prepare my clients due Friday"];

function PortfolioCommands({items,view,client,onFilter,onChange}:{items:WorkItem[];view:string;client:string;onFilter:(view:string)=>void;onChange:()=>void}) {
  const [mode,setMode] = useState<"prepare"|"stop"|"recheck">("prepare");
  const [params] = useSearchParams();
  const goalParam = params.get("goal");
  const [command,setCommand] = useState(goalParam ?? "");
  useEffect(() => { if (goalParam) setCommand(goalParam); }, [goalParam]);
  const [goals,setGoals] = useState<Goal[]>([]);
  useEffect(() => {
    let live = true;
    // A listing is never saved, so one shown before the saved goals load is kept.
    workspaceRequest("/portfolio").then((data: { operations: Goal[] }) => live && setGoals(current => [...current.filter(g => !g.id), ...data.operations])).catch(() => undefined);
    return () => { live = false; };
  }, []);
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
  function interpret(text = command, asked = false) {
    setAnswer("");setAnswerClose("");setError("");
    const askAgent = (fallback: string): Promise<void> => {
      if (asked) {setError(fallback);return Promise.resolve();}
      const labels = [...new Set(items.map(i=>`${i.entity} · ${i.period}`))].slice(0,100);
      setBusy(true);
      return workspaceRequest("/work/interpret",{text:text.trim(),labels}).then(r=>interpret(r.command,true)).catch(e=>setError(e instanceof Error?e.message:"Could not interpret that request.")).finally(()=>setBusy(false));
    };
    if (/^(?:show|list)(?: me)?(?: everything| all work)? waiting (?:on|for) (?:the )?(?:client|clients|replies)$/i.test(text.trim())) {onFilter("waiting_client");return;}
    const named = /^(?:show|list)(?: me)? (decisions|blockers|running work|client requests|prepared work|all work|dependencies)$/i.exec(text.trim());
    if (named) {
      if (named[1].toLowerCase() === "dependencies") {setAnswer("Changed workpapers are listed in Work needing re-review.");return;}
      const views:Record<string,string> = {decisions:"needs_confirmation",blockers:"blocked","running work":"running","client requests":"waiting_client","prepared work":"completed","all work":"all"};
      onFilter(views[named[1].toLowerCase()]);
      return;
    }
    const rehearsal = /^rehearse (.+)$/i.exec(text.trim());
    if (rehearsal) {
      const query = rehearsal[1].trim().toLowerCase();
      const visible = items.find(i=>`${i.entity} · ${i.period}`.toLowerCase() === query);
      if (visible) {setAnswer(`Open the saved production journal rehearsal for ${visible.entity} · ${visible.period}.`);setAnswerClose(visible.close_id);return;}
      setBusy(true);
      void workspaceRequest("").then(data => {
        const matches = (data.closes as {id:string;entity:string;period:string}[]).filter(c=>c.entity.toLowerCase() === query || `${c.entity} · ${c.period}`.toLowerCase() === query);
        if(matches.length !== 1) return askAgent(data.has_more ? "Name an exact client and month among the latest 100 closes, or open Client closes." : "Name exactly one accessible client and month for rehearsal.");
        setAnswer(`Open the saved production journal rehearsal for ${matches[0].entity} · ${matches[0].period}.`);
        setAnswerClose(matches[0].id);
      }).catch(e=>setError(e instanceof Error ? e.message : "Could not find that close.")).finally(()=>setBusy(false));
      return;
    }
    const why = /^why is (.+) waiting\??$/i.exec(text.trim());
    if (why) {
      const query = why[1].trim().toLowerCase();
      const matches = [...new Map(items.filter(i=>i.entity.toLowerCase() === query || `${i.entity} · ${i.period}`.toLowerCase() === query).map(i=>[i.close_id,i])).values()];
      if (matches.length !== 1) {askAgent("Name exactly one visible client and month to explain its work.");return;}
      const related = items.filter(i=>i.close_id === matches[0].close_id);
      setError("");setAnswer(related.map(i=>`${i.source_name}: ${labels[i.state] ?? i.state}${i.blockers.length ? ` — ${i.blockers.join("; ")}` : i.error ? ` — ${i.error}` : ""}`).join("\n"));
      return;
    }
    // A listing the server refuses shows its guidance rather than going to the interpreter.
    const previewGoal = (direct = false) => {
      setBusy(true);
      void workspaceRequest("/portfolio",{text:text.trim(),view,client,today:new Date().toLocaleDateString("en-CA")}).then((goal:Goal)=>{setBusy(false);setGoals(current=>[goal,...current.filter(g=>g.id!==goal.id)]);}).catch(e=>{setBusy(false);const message=e instanceof Error?e.message:"Could not preview that goal.";if(direct){setError(message);}else{void askAgent(message);}});
    };
    if (/^(show|list)\b/i.test(text.trim())) {previewGoal(true);return;}
    if (/^(hand|delegate)\b/i.test(text.trim())) {previewGoal();return;}
    const match = /^(prepare|recheck|stop) (.+)$/i.exec(text.trim());
    if (!match) {askAgent("Try ‘show blockers’, ‘prepare my clients due Friday’, ‘hand over my clients due Friday’, ‘rehearse Client · YYYY-MM’ or ‘why is Client · YYYY-MM waiting?’.");return;}
    const action = match[1].toLowerCase() as "prepare"|"stop"|"recheck";
    const query = match[2].trim().toLowerCase();
    const eligible = closesFor(action);
    const matches = eligible.filter(c=>c.entity === query || c.label.toLowerCase() === query);
    if (matches.length > 1) {setError("Several closes match. Include the close month shown below.");return;}
    if (matches.length !== 1) {previewGoal();return;}
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
  return <AgentComposer
    title="Ask the agent"
    description="Find, explain or prepare work across your clients. Actions always need a preview and confirmation."
    value={command}
    onChange={setCommand}
    onSubmit={() => interpret()}
    busy={busy}
    placeholder="Ask in your own words, such as “prepare my clients due Friday”"
    submitLabel="Find and preview"
    suggestions={suggestions}
    onSuggestion={(text) => { setCommand(text); interpret(text); }}
  >
    <details><summary>What you can ask</summary>
    <p>Ask for a goal such as “prepare my clients due Friday”, “recheck failed work” or “stop work waiting on the client”: the server lists exactly which closes it selects before anything runs. Each close is checked again when it runs and reports its own result. Preparing coordinates existing source jobs; facts and methods still need separate review.</p>
    <p>Ask in your own words, or try “show decisions”, “why is Client · YYYY-MM waiting?”, “rehearse Client · YYYY-MM”, “prepare my clients due Friday” or “stop all visible”.</p>
    </details>
    {answer && <p role="status" className="pre-line">{answer}</p>}
    {answerClose && <Link to={`/dashboard/closes/${answerClose}`}>Open close rehearsal</Link>}
    <details><summary>Choose closes for a portfolio action</summary>
    <label>Command<select value={mode} onChange={e=>{setMode(e.target.value as "prepare"|"stop"|"recheck");setSelected([]);setPreview(undefined);setOutcomes([]);}}><option value="prepare">Prepare selected closes</option><option value="recheck">Recheck failed or pending proposals</option><option value="stop">Stop unresolved work</option></select></label>
    {closes.map(close=><label key={close.id}><input type="checkbox" checked={selected.includes(close.id)} disabled={busy || (!selected.includes(close.id) && selected.length>=10)} onChange={e=>{setPreview(undefined);setOutcomes([]);setSelected(current=>e.target.checked?[...current,close.id]:current.filter(id=>id!==close.id));}} /> {close.label}</label>)}
    <button type="button" disabled={busy || selected.length===0} onClick={()=>void inspect(selected,mode)}>{busy?"Checking…":`Preview ${selected.length} selected close${selected.length===1?"":"s"}`}</button>
    </details>
    {preview && <div><h3>Exact {mode} preview</h3><ul>{preview.map(close=><li key={close.id}>{close.label}: {close.count} {mode === "prepare" ? "source" : "unresolved"} job{close.count===1?"":"s"}</li>)}</ul>{mode === "stop" && <label>Reason for stopping<textarea value={reason} onChange={e=>setReason(e.target.value)} maxLength={500} /></label>}<button type="button" disabled={busy || (mode === "stop" && !reason.trim()) || preview.every(close=>close.count===0)} onClick={()=>void submit()}>{mode === "prepare" ? "Coordinate selected closes" : mode === "recheck" ? "Recheck listed proposals" : "Stop listed work"}</button></div>}
    {goals.map(goal=><PortfolioGoal key={goal.id || goal.goal} goal={goal} onChange={onChange} />)}
    {outcomes.length>0 && <div role="status"><h3>Result by close</h3><ul>{outcomes.map((result,i)=><li key={i}>{result}</li>)}</ul></div>}
    {error && <p role="alert">{error}</p>}
  </AgentComposer>;
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
      <p>
        Durable job #{item.id} · revision {item.revision}. Preparation does not
        approve or post a journal.
      </p>
      {item.agent_checkpoint?.straight_through && <p>{item.agent_checkpoint.straight_through}</p>}
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
                    : `Last link expires ${new Date(latest.link.expires_at).toLocaleString()}.`}
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
