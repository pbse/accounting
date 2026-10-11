import { type ReactNode, useEffect, useRef, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Dialog } from "@close/ui/components/Dialog";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { workPath, workspaceQuery } from "../../data/queries";
import { closeTabs, type CloseTab } from "../../data/closeTabs";
import { monthName, useDates } from "../../data/time";
import { askAssistant } from "../dashboard/assistantStore";
import type { AttentionEvent } from "./Attention";
import { Coordinator } from "./Coordinator";
import { goalNeeds, GoalPanel, handOver, headline, stepWords, useGoal, type Item, type Status } from "./Goal";
import { PlanningControls } from "./Planning";
import { area, needsOf, type Brief, type Counts, type Line } from "./Stands";
import { Work } from "./Work";
import { amount, workspaceRequest } from "./workspace";
import { paperLabel, type Close, type Paper } from "./WorkpaperView";

// A close's Overview (docs/design-system.md, Close page): on the left what needs the person, at most three prepared
// decisions with one action each, then the one table of the agent's work; on the right the same three panels, a few
// figures, the agent, and more on the close. The agent's plan, documents and accounts stay one fold away.

// A close's decisions (GET /{id}/decisions): the events the inbox and the sidebar show, for this close alone, read or
// snoozed included, since neither takes the work off the close.
export function useDecisions(closeId: string) {
  return useQuery({ ...workspaceQuery<{ events: AttentionEvent[] }>(`/${closeId}/decisions`), refetchInterval: 30_000, refetchOnWindowFocus: true });
}

// The goal as the header needs it: is the agent preparing, and is the close still to be handed over. Its light read is
// the one the goal panel polls, so the page reads it once.
export function useGoalStatus(closeId: string) {
  return useQuery({ ...workspaceQuery<Status>(`/${closeId}/goal`), refetchInterval: 15_000 });
}
const preparing = (status: Status | undefined) => status?.goal?.state === "active" && status.progress.items.some((i) => i.status === "working");
const toHandOver = (status: Status | undefined) => !!status && !status.signed_off && (!status.goal || status.goal.state === "stopped");

// One thing for the person: a decision (or several approvals in one review), a blocker from the brief, or handing the
// close to the agent; the one action that settles it, the tab where that is done, and the decisions it stands for.
export type Need = { key: string; title: string; detail: string; action: string; to?: string; act?: () => void; tab?: CloseTab; count: number; blocker?: boolean; review?: string[] };

// Each kind of decision: the line under its title, its one action, and the tab its action is on (none: on Overview).
const kinds: Record<string, { detail: string; action: string; tab?: CloseTab }> = {
  changed: { detail: "Its sources changed, so the draft needs your review again before it counts.", action: "Review again", tab: "workpapers" },
  fix: { detail: "A reviewer asked for changes: revise the draft or prepare a new version.", action: "Open the draft", tab: "workpapers" },
  confirm: { detail: "The agent read these facts and needs you to confirm them before it prepares the draft.", action: "Confirm the facts", tab: "evidence" },
  failed: { detail: "The agent could not prepare it; the document's work says why.", action: "Open the document", tab: "evidence" },
  forwarded: { detail: "Its sender is unverified; it was scanned clean. Release it to the agent, or reject it.", action: "Review the file", tab: "evidence" },
  release: { detail: "Scanned clean, and waiting for you to release it before the agent can use it.", action: "Review the file" },
  held: { detail: "Check the reply and who sent it before it is used.", action: "Review the reply", tab: "requests" },
  answered: { detail: "The reply answers part of the request; the rest is still open.", action: "Read the reply", tab: "requests" },
  send: { detail: "Record how it reached the client, or send it.", action: "Record the delivery", tab: "requests" },
  email: { detail: "Check the address, then send it again.", action: "Fix the delivery", tab: "requests" },
  unanswered: { detail: "No reminder can follow, so it needs you.", action: "Follow up" },
  goal: { detail: "The agent's work shows where it stands.", action: "Open" },
  handoff: { detail: "Handed to you to look into.", action: "Open" },
};
const kindOf = (e: AttentionEvent) => kinds[e.key.startsWith("held:") ? "held" : e.key.startsWith("forwarded:") ? "forwarded" : e.kind] ?? { detail: "", action: "Open" };
const tabOf = (tab: string | undefined) => closeTabs.find(([key]) => key === tab)?.[0];
// Where a brief line's work is: its tab, or on Overview the agent's details, where each document's work is.
// Books' areas open their view: the ledger its captures, the balance sheet the full tie-out, exceptions the agent's
// findings.
const booksViews: Record<string, string> = { ledger: "ledger", "balance sheet": "tie-out", exceptions: "exceptions" };
const briefLink = (line: Line, base: string) =>
  line.tab === "books" && booksViews[line.area] ? `${base}?tab=books&view=${booksViews[line.area]}` : line.tab && line.tab !== "overview" ? `${base}?tab=${line.tab}` : `${base}#agent-details`;
const list = (items: string[]) => (items.length < 3 ? items.join(" and ") : `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`);

// Where an event's action is: the draft's own page, the document's work, coverage, or the tab its kind names.
function target(e: AttentionEvent, base: string) {
  if (e.workpaper_id) return `${base}/workpapers/${e.workpaper_id}`;
  if (e.job_id) return `${base}?job=${e.job_id}`;
  if (e.key.startsWith("forwarded:")) return `${base}?tab=evidence&view=add#forwarded-${e.key.slice("forwarded:".length)}`;
  if (e.account_id || e.kind === "unanswered" || e.key.startsWith("release:")) return `${base}#coverage-work`;
  const tab = kindOf(e).tab;
  return tab ? `${base}?tab=${tab}` : `${base}#agent-details`;
}

// needsFor turns the close's decisions and its brief into what the person sees: the reader's own decisions (the drafts
// ready for approval as one review, each still its own approval), then the brief's blockers that no decision stands for
// (the ledger, the balance sheet, exceptions, sign-off). Notices that need no decision stay out; a teammate's decisions
// are counted apart.
export function needsFor(events: AttentionEvent[] | undefined, brief: Brief | undefined, papers: Paper[], base: string) {
  const open = (events ?? []).filter((e) => !e.info);
  const mine = open.filter((e) => e.yours);
  const needs: Need[] = [];
  const approvals = mine.filter((e) => e.kind === "approve");
  if (approvals.length) {
    const labels = approvals.map((e) => {
      const paper = papers.find((p) => p.id === e.workpaper_id);
      return paper ? `${paperLabel(paper).toLowerCase()} #${paper.id}` : `draft #${e.workpaper_id}`;
    });
    needs.push({
      key: "approvals",
      title: approvals.length === 1 ? `Approve the ${labels[0]}` : `${approvals.length} approvals in one review: ${list(labels)}`,
      detail: approvals.length === 1 ? "Its calculation and sources are ready. Approving does not post." : "Each draft is its own approval, with its calculation and sources ready. Approving does not post.",
      action: "Start review",
      to: `${base}/workpapers/${approvals[0].workpaper_id}`,
      tab: "workpapers",
      count: approvals.length,
      review: approvals.map((e) => e.workpaper_id!),
    });
  }
  for (const e of mine) {
    if (e.kind === "approve") continue;
    const kind = kindOf(e);
    needs.push({ key: e.key, title: e.text, detail: kind.detail, action: kind.action, to: target(e, base), tab: e.workpaper_id ? "workpapers" : kind.tab, count: e.decisions ?? 1 });
  }
  // A brief area that the decisions above stand for (review as drafts, preparation as documents) is not listed again;
  // one with no such decision, such as a close with no documents yet, stays a blocker.
  const covered: Record<string, string[]> = { review: ["approve", "changed", "fix"], preparation: ["confirm", "failed"] };
  for (const line of brief ? needsOf(brief).needs : []) {
    if (open.some((e) => covered[line.area]?.includes(e.kind))) continue;
    needs.push({ key: `brief:${line.area}`, title: area(line), detail: line.text, action: "Open", to: briefLink(line, base), tab: tabOf(line.tab), count: 1, blocker: true });
  }
  const teammates = open.filter((e) => !e.yours).reduce((n, e) => n + (e.decisions ?? 1), 0);
  return { needs, teammates };
}

// What the page counts from the needs: decisions and blockers in their own units, and per tab, for its badge.
export function countsOf(needs: Need[], status: Status | undefined, stale: string): Counts & { tabs: Partial<Record<CloseTab, number>> } {
  const tabs: Partial<Record<CloseTab, number>> = {};
  for (const need of needs) if (need.tab && need.tab !== "overview") tabs[need.tab] = (tabs[need.tab] ?? 0) + need.count;
  return {
    decisions: needs.filter((n) => !n.blocker).reduce((n, need) => n + need.count, 0),
    blockers: needs.filter((n) => n.blocker).length,
    handOver: toHandOver(status),
    preparing: preparing(status),
    stale,
    tabs,
  };
}

// AgentChip says, beside Sign off, that the agent is preparing this close right now.
export function AgentChip({ status }: { status: Status | undefined }) {
  return preparing(status) ? (
    <span className="ks-agent-chip">
      <i aria-hidden="true" />
      Agent preparing
    </span>
  ) : null;
}

// The preparation forms, for work the agent did not prepare from evidence.
function prepareForms(close: Close): [string, string][] {
  const query = `close=${close.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`;
  const accrualWork = close.basis !== "cash" && close.basis !== "modified_cash";
  return [
    ...(accrualWork
      ? ([
          ["Prepaid", `/dashboard/prepaids/new?${query}`],
          ["Accrued expense", `/dashboard/accruals/new?${query}`],
          ["Deferred revenue", `/dashboard/prepaids/new?${query}&schedule=deferred_revenue`],
          ["Depreciation", `/dashboard/prepaids/new?${query}&schedule=depreciation`],
        ] as [string, string][])
      : []),
    ["Loan schedule", `/dashboard/loans/new?${query}`],
    ["Owner item", `/dashboard/owners/new?${query}`],
    ["Payroll", `/dashboard/payroll/new?${query}`],
    ["Statement reconciliation", `/dashboard/reconciliations/new?${query}`],
    ["Stripe payouts", `/dashboard/settlements/new?${query}`],
  ];
}

// CloseMenu is the close's ⋯ menu: its plan, preparing a draft by hand, and stopping the agent, the actions and
// exception paths that are not the page's work.
export function CloseMenu({ close, status, onPlanSaved, tab }: { close: Close; status: Status | undefined; onPlanSaved: () => void; tab?: { title: string; items: { label: string; to: string }[] } }) {
  const [open, setOpen] = useState(false);
  const [dialog, setDialog] = useState<"" | "plan" | "prepare" | "stop">("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const menu = useRef<HTMLDivElement>(null);
  const active = status?.goal?.state === "active";
  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
    const away = (e: PointerEvent) => !menu.current?.contains(e.target as Node) && setOpen(false);
    const escape = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", escape);
    };
  }, [open]);
  const pick = (which: typeof dialog) => {
    setOpen(false);
    setError("");
    setDialog(which);
  };
  async function stop() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${close.id}/goal/stop`, { reason: reason.trim() });
      setReason("");
      setDialog("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "The agent could not be stopped.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="ks-close-more" ref={menu}>
      <button type="button" className="ks-ib" aria-label="More for this close" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>
        <Icon name="more" />
      </button>
      {open && (
        <div className="ks-menu" role="menu" aria-label="This close">
          {tab && (
            <>
              <div className="ks-menu-head">{tab.title}</div>
              {tab.items.map((item) => (
                <Link key={item.label} role="menuitem" to={item.to} onClick={() => setOpen(false)}>
                  {item.label}
                </Link>
              ))}
              <div className="ks-sep" role="separator" />
            </>
          )}
          <div className="ks-menu-head">This close</div>
          <button type="button" role="menuitem" onClick={() => pick("plan")}>
            Plan this close
          </button>
          <button type="button" role="menuitem" onClick={() => pick("prepare")}>
            Prepare a draft by hand
          </button>
          {active && (
            <>
              <div className="ks-sep" role="separator" />
              <button type="button" role="menuitem" className="danger" onClick={() => pick("stop")}>
                Stop the agent
              </button>
            </>
          )}
        </div>
      )}
      <Dialog open={dialog === "plan"} onClose={() => setDialog("")} title="Plan this close" description="Its due date, priority, and who holds it.">
        <PlanningControls
          key={`${close.due_on}-${close.priority}-${close.owned_by_me}-${close.assigned}-${close.can_assign}-${close.can_take_over}`}
          closeId={close.id}
          plan={close}
          onSaved={() => {
            setDialog("");
            onPlanSaved();
          }}
        />
      </Dialog>
      <Dialog open={dialog === "prepare"} onClose={() => setDialog("")} title="Prepare a draft by hand" description="For work the agent did not prepare from evidence.">
        <nav className="ks-links" aria-label="Prepare by hand">
          {prepareForms(close).map(([label, to]) => (
            <Link key={to} to={to}>
              <span>{label}</span>
              <Icon name="chevron" size={14} />
            </Link>
          ))}
        </nav>
      </Dialog>
      <Dialog open={dialog === "stop"} onClose={() => setDialog("")} title="Stop the agent" description="Work already started keeps running; stopping only takes the close back from the agent.">
        <label>
          Reason
          <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
        </label>
        <button type="button" className="danger" disabled={busy || !reason.trim()} onClick={() => void stop()}>
          {busy ? "Stopping…" : "Stop the agent"}
        </button>
        {error && <p role="alert">{error}</p>}
      </Dialog>
    </div>
  );
}

// A fold line over brief lines that need no decision: what waits on others, what could not be checked, what is done.
function Fold({ icon, title, lines, base, children }: { icon: "waiting" | "blocked" | "check"; title: string; lines: Line[]; base: string; children?: ReactNode }) {
  return (
    <details className={`ks-done ${icon === "check" ? "" : icon === "blocked" ? "unchecked" : "waiting"}`}>
      <summary className="ks-done-line">
        <Icon name={icon} />
        {title}
        {children}
        <Icon name="chevron" className="ks-chev" size={14} />
      </summary>
      <div className="ks-done-list">
        {lines.map((line) => (
          <div className="ks-done-row" key={line.area}>
            <span className={`status ${line.state === "done" ? "ok" : line.state === "unknown" ? "danger" : "info"}`}>{line.state === "done" ? "Done" : line.state === "unknown" ? "Not checked" : line.state === "waiting" ? "Waiting" : "Note"}</span>
            <b>{area(line)}</b>
            <span>{line.text}</span>
            <Link to={briefLink(line, base)}>Open</Link>
          </div>
        ))}
        {lines.length === 0 && <p className="empty">Nothing yet.</p>}
      </div>
    </details>
  );
}

type Review = { state: string; digest: string; review_id: string };
type WorkItem = { workpaper_ids?: string[]; source_name?: string; agent_checkpoint?: { straight_through?: string } };
type TieRow = { account: string; expected: string | null; difference: string | null; status: string; provisional: boolean; supports: { paper_id: string }[]; entries: { paper_id: string }[] };
type Entry = { date: string; debit_account: string; credit_account: string; amount: string };
type Effect = { entry: Entry; role: string };

// The journal entries a draft proposes, whatever its kind, each with its date and what it is: an accrual's reversal
// and a disposal fall in another month than the adjustment. A reconciliation proposes none.
export function entriesOf(p: Paper): Effect[] {
  const one = (entry: Entry | null | undefined, role = "") => (entry ? [{ entry, role }] : []);
  switch (p.kind) {
    case "prepaid":
      return [...one(p.paper.proposal), ...(p.paper.disposal ?? []).map((entry) => ({ entry, role: "disposal, booked by hand" }))];
    case "accrual":
      return [...one(p.paper.proposal), ...one(p.paper.reversal, "reversal")];
    case "owner":
      return one(p.paper.proposal);
    case "loan":
    case "payroll":
      return p.paper.proposals.map((entry) => ({ entry, role: "" }));
    case "settlement":
      return [...one(p.paper.proposal), ...one(p.paper.refund_proposal, "refunds"), ...one(p.paper.revenue_proposal, "revenue")];
  }
  return [];
}

// Where a balance this draft supports stands, as the tie-out reads it: from every draft that supports the account, not
// from this one alone, and whether the entries must still post or the capture is provisional.
function tieWords(row: TieRow, id: string, currency: string) {
  const others = new Set([...row.supports.map((s) => s.paper_id), ...row.entries.map((e) => e.paper_id)]);
  others.delete(id);
  const state =
    row.status === "ties"
      ? `ties at ${amount(row.expected ?? "", currency)}`
      : row.status === "ties_after_entries"
        ? `ties at ${amount(row.expected ?? "", currency)} once the entries post`
        : row.status === "within_materiality"
          ? "is within materiality"
          : row.status === "differs"
            ? `still differs by ${amount(row.difference ?? "", currency)}`
            : "";
  if (!state) return "";
  const drafts = others.size ? `with this and ${others.size} other draft${others.size === 1 ? "" : "s"}` : "with this draft";
  return `${row.account}, ${drafts}, ${state}${row.provisional ? " (provisional)" : ""}`;
}

// InPlaceReview opens one draft's approval where the decision is listed: what the agent did, its effect on the books
// and whether the balance then ties, its sources, then Approve (one click; approving does not post) or Return with a
// note. A payroll recording is approved on its own page, with its checks and its two confirmations together.
function InPlaceReview({ closeId, base, listed, id, position, total, onDone }: { closeId: string; base: string; listed: Paper | undefined; id: string; position: number; total: number; onDone: (result: "approved" | "returned") => void }) {
  const dates = useDates();
  // A draft older than the page's latest hundred is read on its own: approval waits until its effect and sources show.
  const record = useQuery({ ...workspaceQuery<Paper>(`/${closeId}/workpapers/${id}?record=1`), enabled: !listed });
  const paper = listed ?? record.data;
  const review = useQuery(workspaceQuery<Review>(`/${closeId}/workpapers/${id}/reviews`));
  const work = useQuery(workspaceQuery<{ items: WorkItem[] }>(workPath(closeId, "all")));
  const tie = useQuery(workspaceQuery<{ rows: TieRow[]; currency?: string }>(`/${closeId}/tie-out`));
  const [returning, setReturning] = useState(false);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const card = useRef<HTMLDivElement>(null);
  useEffect(() => card.current?.focus(), [id]);
  const label = paper ? `${paperLabel(paper)} #${id}` : `Draft #${id}`;
  const currency = (paper?.paper as { input?: { currency?: string } } | undefined)?.input?.currency ?? tie.data?.currency ?? "USD";
  const item = work.data?.items.find((i) => i.workpaper_ids?.includes(id));
  const did = item?.agent_checkpoint?.straight_through || (item?.source_name ? `Prepared from ${item.source_name}.` : "");
  const entries = paper ? entriesOf(paper) : [];
  const ties = (tie.data?.rows ?? []).filter((row) => row.supports.some((s) => s.paper_id === id) || row.entries.some((e) => e.paper_id === id)).map((row) => tieWords(row, id, currency)).filter(Boolean);
  const changed = review.data?.state === "source_changed";
  async function decide(decision: "approved" | "changes_requested") {
    if (!review.data) return;
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/workpapers/${id}/reviews`, { decision, note: decision === "approved" ? "" : note.trim(), digest: review.data.digest, expected_review_id: review.data.review_id });
      onDone(decision === "approved" ? "approved" : "returned");
    } catch (e) {
      setError(e instanceof Error ? e.message : "The decision was not recorded. Refresh and try again.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="ks-decision" role="region" aria-label={`Approval ${position} of ${total} · ${label}`} tabIndex={-1} ref={card}>
      <div className="ks-dec-head">
        <span className="ks-numeral">1</span>
        <div>
          <b>
            Approval {position} of {total} · {label}
          </b>
          {paper && <p>Prepared {dates.dateTime(paper.created_at)}</p>}
        </div>
        <Link to={`${base}/workpapers/${id}`}>Open in Workpapers</Link>
      </div>
      <div className="ks-dec-body">
        <div>
          <h3 className="ks-label">What the agent did</h3>
          <p>{did || (work.isPending ? "…" : "No document's work names this draft; its calculation and inputs are on its page.")}</p>
        </div>
        <div>
          <h3 className="ks-label">Effect on the books</h3>
          {entries.length ? (
            <dl className="ks-dl">
              {entries.map(({ entry: e, role }, n) => (
                <div key={n}>
                  <dt className="when">
                    {e.date}
                    {role ? ` · ${role}` : ""}
                  </dt>
                  <dt>{e.debit_account}</dt>
                  <dd>Dr {amount(e.amount, currency)}</dd>
                  <dt>{e.credit_account}</dt>
                  <dd>Cr {amount(e.amount, currency)}</dd>
                </div>
              ))}
              {ties.map((words) => (
                <dd className="tie" key={words}>
                  {words}
                </dd>
              ))}
            </dl>
          ) : (
            <p>{paper ? "No journal entry: this draft records its result only." : record.isError ? "The draft could not be read." : "…"}</p>
          )}
        </div>
        <div>
          <h3 className="ks-label">Sources</h3>
          <ul className="ks-cites">
            {(paper?.paper.sources ?? []).map((source) => (
              <li key={source.id}>
                {source.name}
                {source.location ? ` · ${source.location}` : ""}
              </li>
            ))}
          </ul>
        </div>
      </div>
      {returning && (
        <label className="ks-dec-note">
          What needs changing
          <textarea value={note} maxLength={2000} onChange={(e) => setNote(e.target.value)} autoFocus />
        </label>
      )}
      <div className="ks-dec-foot">
        {paper?.kind === "payroll" ? (
          <Link className="button primary" to={`${base}/workpapers/${id}`}>
            Review payroll
          </Link>
        ) : returning ? (
          <>
            <button type="button" className="primary" disabled={busy || !note.trim() || !review.data} onClick={() => void decide("changes_requested")}>
              {busy ? "Returning…" : "Return with this note"}
            </button>
            <button type="button" disabled={busy} onClick={() => setReturning(false)}>
              Cancel
            </button>
          </>
        ) : (
          <>
            <button type="button" className="primary" disabled={busy || changed || !review.data || !paper} onClick={() => void decide("approved")}>
              {busy ? "Approving…" : "Approve"}
            </button>
            <button type="button" disabled={busy || !review.data} onClick={() => setReturning(true)}>
              Return with a note
            </button>
          </>
        )}
        <span className="ks-quiet">{paper?.kind === "payroll" ? "Payroll is approved with its two confirmations on its page." : changed ? "Its sources changed: open it in Workpapers to review it again." : "Approving does not post. Posting is its own step."}</span>
      </div>
      {(error || review.error) && <p role="alert">{error || review.error?.message || "The review could not be read."}</p>}
    </div>
  );
}

// NeedsYou lists what needs the person, at most three at a time, the first one's action the page's one primary; the
// rest fold to a line. Drafts ready for approval open in place, one after another; each outcome stays as a line, and
// the next decision is marked up next. When nothing needs the person, one calm line says so, and why (the agent may
// still be preparing). What a teammate's role decides, what waits on others, what could not be checked and what is
// done each fold to a line.
export function NeedsYou({ closeId, papers, needs, teammates, brief, briefRead, decisionsError, base, preparing }: { closeId: string; papers: Paper[]; needs: Need[]; teammates: number; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; base: string; preparing: boolean }) {
  const dates = useDates();
  const [all, setAll] = useState(false);
  const [session, setSession] = useState<{ ids: string[]; results: ("approved" | "returned")[] }>();
  const labelOf = (id: string) => {
    const paper = papers.find((p) => p.id === id);
    return paper ? `${paperLabel(paper).toLowerCase()} #${id}` : `draft #${id}`;
  };
  // While a review is open its drafts are the session's, not the list's. Approvals that arrived since stay listed as more
  // approvals, which continue the same session.
  const listed = !session
    ? needs
    : needs.flatMap((need) => {
        if (!need.review) return [need];
        const more = need.review.filter((id) => !session.ids.includes(id));
        return more.length ? [{ ...need, key: "approvals-more", review: more, count: more.length, title: more.length === 1 ? `Approve the ${labelOf(more[0])}` : `${more.length} more approvals in one review: ${list(more.map(labelOf))}` }] : [];
      });
  const shown = all ? listed : listed.slice(0, session ? 2 : 3);
  const rest = listed.slice(shown.length);
  const total = needs.reduce((n, need) => n + need.count, 0);
  const lines = brief?.lines ?? [];
  const waiting = lines.filter((l) => l.state === "waiting");
  const unchecked = lines.filter((l) => l.state === "unknown");
  const done = lines.filter((l) => l.state === "done" || l.state === "info");
  const finished = !!session && session.results.length === session.ids.length;
  const approved = session?.results.filter((r) => r === "approved").length ?? 0;
  const offset = session ? 1 : 0;
  return (
    <section className="bare ks-needs" aria-labelledby="needs-title">
      <h2 className="ks-label" id="needs-title">
        Needs your decision
        {total > 0 && <span className="ks-badge">{total}</span>}
      </h2>
      {decisionsError && <p role="alert">{decisionsError} The close's decisions could not be read; this list may be incomplete.</p>}
      <div className="ks-tasks">
        {session && (
          <div className="ks-session" role="status">
            {session.results.map((result, n) => (
              <p className={result === "returned" ? "ks-outcome returned" : "ks-outcome"} key={session.ids[n]}>
                <Icon name={result === "returned" ? "back" : "check"} />
                {result === "returned" ? `Returned ${labelOf(session.ids[n])} with your note` : `Approved ${labelOf(session.ids[n])}`} · {n + 1} of {session.ids.length}
              </p>
            ))}
            {finished && (
              <p className={approved === session.ids.length ? "ks-outcome" : "ks-outcome returned"}>
                <Icon name="check" />
                {approved === session.ids.length ? `${approved === 1 ? "Approved" : `All ${approved} approved`} · posting is its own step` : `${approved} of ${session.ids.length} approved · what you returned goes back with your note`}
              </p>
            )}
          </div>
        )}
        {session && !finished && (
          <InPlaceReview
            key={session.ids[session.results.length]}
            closeId={closeId}
            base={base}
            id={session.ids[session.results.length]}
            listed={papers.find((p) => p.id === session.ids[session.results.length])}
            position={session.results.length + 1}
            total={session.ids.length}
            onDone={(result) => setSession({ ...session, results: [...session.results, result] })}
          />
        )}
        {shown.map((need, i) => {
          // The first action is the page's one primary, unless a review is open; once it is done, the next one is.
          const primary = i === 0 && (!session || finished);
          return (
            <div className="ks-task" key={need.key}>
              <span className="ks-numeral">{i + 1 + offset}</span>
              <div>
                <b>
                  {need.title}
                  {i === 0 && finished && <span className="pill neutral ks-up-next">Up next</span>}
                </b>
                <p>{need.detail}</p>
              </div>
              {need.review ? (
                <button type="button" className={primary ? "primary" : undefined} onClick={() => setSession(session ? { ids: [...session.ids, ...need.review!], results: session.results } : { ids: need.review!, results: [] })}>
                  {need.action}
                </button>
              ) : need.act ? (
                <button type="button" className={primary ? "primary" : undefined} onClick={need.act}>
                  {need.action}
                </button>
              ) : need.to ? (
                <Link className={primary ? "button primary" : "button"} to={need.to}>
                  {need.action}
                </Link>
              ) : (
                <span />
              )}
            </div>
          );
        })}
        {needs.length === 0 && !session && !decisionsError && (
          <p className="ks-calm">
            <Icon name="check" />
            {preparing ? "Nothing needs you now. The agent is preparing, and brings each decision here when it is ready." : "Nothing needs you now."}
          </p>
        )}
        {rest.length > 0 && (
          <button type="button" className="ks-fold" aria-expanded={all} onClick={() => setAll(!all)}>
            <Icon name="arrow" />
            {all ? "Show fewer" : `${rest.reduce((n, need) => n + need.count, 0)} more · ${list([...new Set(rest.map((need) => need.action.toLowerCase()))])}`}
            <Icon name="chevron" className="ks-chev" size={14} />
          </button>
        )}
        {teammates > 0 && (
          <p className="ks-fold wait">
            <Icon name="people" />
            {teammates} {teammates === 1 ? "decision waits" : "decisions wait"} on a teammate's role
          </p>
        )}
        {unchecked.length > 0 && <Fold icon="blocked" title={`Not checked · ${unchecked.map((l) => l.area).join(", ")}`} lines={unchecked} base={base} />}
        {waiting.length > 0 && <Fold icon="waiting" title={`Waiting · ${waiting.map((l) => l.area).join(", ")}`} lines={waiting} base={base} />}
        {brief && (
          <Fold icon="check" title={done.some((l) => l.state === "done") ? `${done.filter((l) => l.state === "done").length} done · ${done.filter((l) => l.state === "done").map((l) => l.area).join(", ")}` : "Nothing done yet"} lines={done} base={base}>
            <small>
              As of {dates.time(briefRead.dataUpdatedAt)}
              {" · "}
              <button
                type="button"
                className="ks-link"
                disabled={briefRead.isFetching}
                onClick={(event) => {
                  event.preventDefault();
                  void briefRead.refetch();
                }}
              >
                Refresh
              </button>
            </small>
          </Fold>
        )}
      </div>
    </section>
  );
}

// A document's place in the agent's work, in the close's status words: Preparing, Needs your decision, Waiting for
// client, Ready for review, Approved (approval is not posting), or why it is out of the work. A prepared document's
// status is its drafts' review.
function itemStatus(item: Item, papers: Paper[]): [string, string] {
  switch (item.status) {
    case "working":
      return ["Preparing", "live"];
    case "needs_you":
      return ["Needs your decision", "muted"];
    case "client":
      return ["Waiting for client", "muted"];
    case "unsupported":
      return ["Can't prepare", "danger"];
    case "stopped":
      return ["Left out", "neutral"];
  }
  const states = item.drafts.map((id) => papers.find((p) => p.id === id)?.review_state);
  if (states.includes("source_changed")) return ["Source changed", "danger"];
  if (states.includes("changes_requested")) return ["Changes requested", "danger"];
  if (states.length > 0 && states.every((s) => s === "approved")) return ["Approved", ""];
  return ["Ready for review", "muted"];
}
const order = ["Preparing", "Needs your decision", "Waiting for client", "Ready for review", "Source changed", "Changes requested", "Approved", "Can't prepare", "Left out"];
const where = (item: Item) => (item.drafts.length ? "Workpapers" : item.status === "client" ? "Requests" : "Documents");

// AgentWork is the tab's one table: each document the agent works from and where it stands, filtered by status. Before
// the close is handed over it is the agent's plan instead, step by step, with who each step waits on.
function AgentWork({ status, papers, base }: { status: Status; papers: Paper[]; base: string }) {
  const [filter, setFilter] = useState("all");
  const { goal, progress } = status;
  if (!goal) {
    return (
      <section className="bare ks-set" aria-labelledby="work-title">
        <div className="ks-sec">
          <h2 id="work-title">What the agent will do</h2>
          <small>{progress.plan.length} steps, once you hand the close over</small>
        </div>
        <div className="ks-table ks-plan" role="table" aria-labelledby="work-title">
          <div className="ks-tr head" role="row">
            <span role="columnheader">Step</span>
            <span role="columnheader">Status</span>
            <span role="columnheader">Where it stands</span>
          </div>
          {progress.plan.map((step) => (
            <div className="ks-tr" role="row" key={step.key}>
              <span className="ks-who" role="cell">
                {step.link ? <Link to={step.link}>{step.title}</Link> : step.title}
              </span>
              <span role="cell">
                <span className={`pill ${step.state === "done" ? "" : step.state === "you" || step.state === "client" ? "muted" : step.state === "unknown" ? "danger" : "neutral"}`}>{step.state === "agent" ? "Planned" : stepWords[step.state][1]}</span>
              </span>
              <span className="ks-mut" role="cell">
                {step.text}
              </span>
            </div>
          ))}
        </div>
      </section>
    );
  }
  const rows = progress.items.map((item) => ({ item, status: itemStatus(item, papers) }));
  const counts = new Map<string, number>();
  for (const row of rows) counts.set(row.status[0], (counts.get(row.status[0]) ?? 0) + 1);
  const shown = filter === "all" ? rows : rows.filter((row) => row.status[0] === filter);
  return (
    <section className="bare ks-set" aria-labelledby="work-title">
      <div className="ks-sec">
        <h2 id="work-title">The agent's work</h2>
        <small>{rows.length === 1 ? "1 document" : `${rows.length} documents`}</small>
        {rows.length > 0 && (
          <div className="ks-end">
            <Segmented label="Filter the agent's work" items={[{ key: "all", label: "All", count: rows.length }, ...order.filter((label) => counts.has(label)).map((label) => ({ key: label, label, count: counts.get(label) }))]} value={counts.has(filter) ? filter : "all"} onChange={setFilter} />
          </div>
        )}
      </div>
      <div className="ks-table ks-work" role="table" aria-labelledby="work-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Document</span>
          <span role="columnheader">Status</span>
          <span role="columnheader">Where</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {shown.map(({ item, status: [label, tone] }) => (
          <div className="ks-tr linked" role="row" key={item.job_id}>
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={item.drafts[0] ? `${base}/workpapers/${item.drafts[0]}` : `${base}?job=${item.job_id}`} title={item.text}>
                {item.name}
              </Link>
            </span>
            <span role="cell">
              <span className={`pill ${tone}`}>{label}</span>
            </span>
            <span className="ks-mut" role="cell">
              {where(item)}
            </span>
            <span role="cell">
              <Icon name="arrow" className="ks-go" />
            </span>
          </div>
        ))}
        {rows.length === 0 && <p className="ks-empty-row">No documents yet. The agent picks up each one as it arrives.</p>}
      </div>
    </section>
  );
}

type Effort = { tracked_seconds: number };
// A figure on the rail: its value, what it counts, and "att" when it needs attention.
export type Figure = [string, string, string?];
export type MoreLink = { label: string; note?: string; to: string };

// Glance is the rail's first panel: a few figures found nowhere else on the page.
export function Glance({ figures }: { figures: Figure[] }) {
  return (
    <section className="ks-panel" aria-labelledby="glance-title">
      <h2 className="ks-label" id="glance-title">
        At a glance
      </h2>
      {figures.length ? (
        <div className="ks-figs">
          {figures.map(([value, label, tone]) => (
            <div className={`ks-fig ${tone ?? ""}`} key={label}>
              <b>{value}</b>
              <span>{label}</span>
            </div>
          ))}
        </div>
      ) : (
        <p className="ks-quiet">Nothing to count yet.</p>
      )}
    </section>
  );
}

// AgentPanel is the agent in one line, with its activity folded behind a link.
export function AgentPanel({ close, status }: { close: Close; status: Status | undefined }) {
  const dates = useDates();
  const [history, setHistory] = useState(false);
  const [all, setAll] = useState<Status["activity"]>();
  const [error, setError] = useState("");
  const latest = status?.activity[0];
  const total = status?.activity_total ?? 0;
  const line = status?.goal ? headline(status.goal, status.progress, goalNeeds(status)) : status?.signed_off ? "Signed off; nothing left to prepare." : status ? "Not handed over yet." : "";
  return (
    <section className="ks-panel" aria-labelledby="agent-title">
      <h2 className="ks-label" id="agent-title">
        The agent
      </h2>
      <p className="ks-agent-line">
        <i className={preparing(status) ? "live" : undefined} aria-hidden="true" />
        <span>
          {line}
          {latest && (
            <small>
              {latest.text} · {dates.dateTime(latest.at)}
            </small>
          )}
        </span>
      </p>
      {total > 0 && (
        <button type="button" className="ks-act-link" onClick={() => setHistory(true)}>
          Activity · {total}
        </button>
      )}
      <Dialog open={history} onClose={() => setHistory(false)} title="What happened" description={`${close.entity}, ${monthName(close.period)}`}>
        <ol className="ks-activity">
          {(all?.length === total ? all : (status?.activity ?? [])).map((a, n) => (
            <li key={n}>
              <small>{dates.dateTime(a.at)}</small> {a.text}
            </li>
          ))}
        </ol>
        {all?.length !== total && total > (status?.activity.length ?? 0) && (
          <button type="button" onClick={() => void workspaceRequest(`/${close.id}/goal?activity=all`).then((d: Status) => setAll(d.activity), () => setError("The full history could not be read."))}>
            Show all {total}
          </button>
        )}
        {error && <p role="alert">{error}</p>}
      </Dialog>
    </section>
  );
}

// MoreLinks is the rail's last panel: the tab's other views, or more on the close.
export function MoreLinks({ title, links }: { title: string; links: MoreLink[] }) {
  return (
    <section className="ks-panel" aria-labelledby="more-title">
      <h2 className="ks-label" id="more-title">
        {title}
      </h2>
      <nav className="ks-links" aria-labelledby="more-title">
        {links.map((link) => (
          <Link to={link.to} key={link.to}>
            <span>{link.label}</span>
            {link.note && <small>{link.note}</small>}
            <Icon name="chevron" size={14} />
          </Link>
        ))}
      </nav>
    </section>
  );
}

// Rail is the same three panels on every tab; on Overview: what is approved, covered and in preparation, the time
// spent, the agent, and more on this close.
function Rail({ close, status, papers, brief, base }: { close: Close; status: Status | undefined; papers: Paper[]; brief: Brief | undefined; base: string }) {
  const effort = useQuery(workspaceQuery<Effort>(`/${close.id}/effort`));
  const current = papers.filter((p) => !papers.some((q) => q.paper.revises === p.id));
  const approved = current.filter((p) => p.review_state === "approved").length;
  const coverage = status?.progress.coverage?.total ? status.progress.coverage : undefined;
  const working = status?.goal?.state === "active" ? status.progress.items.filter((i) => i.status === "working").length : 0;
  const figures: Figure[] = [
    ...(current.length ? ([[`${approved} of ${current.length}`, "drafts approved"]] as Figure[]) : []),
    ...(coverage ? ([[`${coverage.total - coverage.open} of ${coverage.total}`, "accounts covered", coverage.open ? "att" : ""]] as Figure[]) : []),
    ...(working ? ([[String(working), working === 1 ? "document the agent is preparing" : "documents the agent is preparing"]] as Figure[]) : []),
    ...(effort.data && effort.data.tracked_seconds > 0 ? ([[`${Math.max(1, Math.round(effort.data.tracked_seconds / 60))} min`, "time spent on this close"]] as Figure[]) : []),
  ];
  // The sign-off line's own words, without repeating the row's name.
  const signOff = brief?.lines.find((l) => l.area === "sign-off")?.text.replace(/^Sign-off\s+/i, "");
  const next = status?.progress.signoff?.next_close;
  return (
    <aside className="ks-rail" aria-label="About this close">
      <Glance figures={figures} />
      <AgentPanel close={close} status={status} />
      <MoreLinks
        title="More on this close"
        links={[
          { label: "Sign-off checks", note: signOff ? signOff[0].toUpperCase() + signOff.slice(1) : undefined, to: `${base}?tab=signoff` },
          { label: "The agent's plan, documents and accounts", to: `${base}#agent-details` },
          ...(next ? [{ label: "Next month's close", to: `/dashboard/closes/${next}` }] : []),
        ]}
      />
    </aside>
  );
}

// AgentDetails keeps the agent's plan, hand-offs and routine, the close's coverage and the work on each document one
// fold away, open when a link points into it (a document's work, coverage, or the fold itself).
function AgentDetails({ closeId, onPrepared }: { closeId: string; onPrepared: () => void }) {
  const location = useLocation();
  const [params] = useSearchParams();
  const linked = !!params.get("job") || ["#agent-details", "#coverage-work", "#close-work"].includes(location.hash) || location.hash.startsWith("#work-");
  const [open, setOpen] = useState(linked);
  useEffect(() => {
    if (linked) setOpen(true);
  }, [linked, location.key]);
  return (
    <details id="agent-details" className="ks-done ks-details" open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary className="ks-done-line">
        <Icon name="agent" />
        The agent's plan, documents and accounts
        <Icon name="chevron" className="ks-chev" size={14} />
      </summary>
      {open && (
        <div className="stack">
          <GoalPanel key={`goal-${closeId}`} closeId={closeId} />
          <Coordinator key={`coordinator-${closeId}`} closeId={closeId} />
          <Work key={`work-${closeId}`} closeId={closeId} onPrepared={onPrepared} />
        </div>
      )}
    </details>
  );
}

// Overview is the page's body for its first tab: the needs and the agent's work on the left, the rail on the right.
export function Overview({ close, papers, needs, teammates, brief, briefRead, decisionsError, base, onPrepared }: { close: Close; papers: Paper[]; needs: Need[]; teammates: number; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; base: string; onPrepared: () => void }) {
  const { status, error } = useGoal(close.id, true, 10000);
  const handing = toHandOver(status)
    ? [{ key: "hand-over", title: `Hand ${monthName(close.period)} to the agent`, detail: "It keeps preparation moving and brings you only what needs you. It never approves, sends, posts or signs off.", action: status?.goal ? "Hand it over again" : "Hand over", act: () => askAssistant(handOver, close.id), count: 1 }]
    : [];
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={[...handing, ...needs]} teammates={teammates} brief={brief} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={preparing(status)} />
        {status ? <AgentWork status={status} papers={papers} base={base} /> : error ? <p role="alert">{error}</p> : null}
        <AgentDetails closeId={close.id} onPrepared={onPrepared} />
      </div>
      <Rail close={close} status={status} papers={papers} brief={brief} base={base} />
    </div>
  );
}
