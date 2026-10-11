import { type ReactNode, useEffect, useRef, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Dialog } from "@close/ui/components/Dialog";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { workspaceQuery } from "../../data/queries";
import { closeTabs, type CloseTab } from "../../data/closeTabs";
import { monthName, useDates } from "../../data/time";
import { askAssistant } from "../dashboard/assistantStore";
import type { AttentionEvent } from "./Attention";
import { Coordinator } from "./Coordinator";
import { goalNeeds, GoalPanel, handOver, headline, stepWords, useGoal, type Item, type Status } from "./Goal";
import { PlanningControls } from "./Planning";
import { area, needsOf, type Brief, type Counts, type Line } from "./Stands";
import { Work } from "./Work";
import { workspaceRequest } from "./workspace";
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
export const preparing = (status: Status | undefined) => status?.goal?.state === "active" && status.progress.items.some((i) => i.status === "working");
export const toHandOver = (status: Status | undefined) => !!status && !status.signed_off && (!status.goal || status.goal.state === "stopped");

// One thing for the person: a decision (or several approvals in one review), a blocker from the brief, or handing the
// close to the agent; the one action that settles it, the tab where that is done, and the decisions it stands for.
export type Need = { key: string; title: string; detail: string; action: string; to?: string; act?: () => void; tab?: CloseTab; count: number; blocker?: boolean };

// Each kind of decision: the line under its title, its one action, and the tab its action is on (none: on Overview).
const kinds: Record<string, { detail: string; action: string; tab?: CloseTab }> = {
  changed: { detail: "Its sources changed, so the draft needs your review again before it counts.", action: "Review again", tab: "workpapers" },
  fix: { detail: "A reviewer asked for changes: revise the draft or prepare a new version.", action: "Open the draft", tab: "workpapers" },
  confirm: { detail: "The agent read these facts and needs you to confirm them before it prepares the draft.", action: "Confirm the facts" },
  failed: { detail: "The agent could not prepare it; the document's work says why.", action: "Open the document" },
  release: { detail: "Scanned clean, and waiting for you to release it before the agent can use it.", action: "Review the file" },
  held: { detail: "Check the reply and who sent it before it is used.", action: "Review the reply", tab: "requests" },
  answered: { detail: "The reply answers part of the request; the rest is still open.", action: "Read the reply", tab: "requests" },
  send: { detail: "Record how it reached the client, or send it.", action: "Record the delivery", tab: "requests" },
  email: { detail: "Check the address, then send it again.", action: "Fix the delivery", tab: "requests" },
  unanswered: { detail: "No reminder can follow, so it needs you.", action: "Follow up" },
  goal: { detail: "The agent's work shows where it stands.", action: "Open" },
  handoff: { detail: "Handed to you to look into.", action: "Open" },
};
const kindOf = (e: AttentionEvent) => kinds[e.key.startsWith("held:") ? "held" : e.kind] ?? { detail: "", action: "Open" };
const tabOf = (tab: string | undefined) => closeTabs.find(([key]) => key === tab)?.[0];
// Where a brief line's work is: its tab, or on Overview the agent's details, where each document's work is.
const briefLink = (line: Line, base: string) => (line.tab && line.tab !== "overview" ? `${base}?tab=${line.tab}` : `${base}#agent-details`);
const list = (items: string[]) => (items.length < 3 ? items.join(" and ") : `${items.slice(0, -1).join(", ")} and ${items[items.length - 1]}`);

// Where an event's action is: the draft's own page, the document's work, coverage, or the tab its kind names.
function target(e: AttentionEvent, base: string) {
  if (e.workpaper_id) return `${base}/workpapers/${e.workpaper_id}`;
  if (e.job_id) return `${base}?job=${e.job_id}`;
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
export function CloseMenu({ close, status, onPlanSaved }: { close: Close; status: Status | undefined; onPlanSaved: () => void }) {
  const [open, setOpen] = useState(false);
  const [dialog, setDialog] = useState<"" | "plan" | "prepare" | "stop">("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const menu = useRef<HTMLDivElement>(null);
  const active = status?.goal?.state === "active";
  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus();
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

// NeedsYou lists what needs the person, at most three at a time, the first one's action the page's one primary; the
// rest fold to a line. When nothing does, one calm line says so, and why (the agent may still be preparing). What a
// teammate's role decides, what waits on others, what could not be checked and what is done each fold to a line.
function NeedsYou({ needs, teammates, brief, briefRead, decisionsError, base, preparing }: { needs: Need[]; teammates: number; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; base: string; preparing: boolean }) {
  const dates = useDates();
  const [all, setAll] = useState(false);
  const shown = all ? needs : needs.slice(0, 3);
  const rest = needs.slice(3);
  const total = needs.reduce((n, need) => n + need.count, 0);
  const lines = brief?.lines ?? [];
  const waiting = lines.filter((l) => l.state === "waiting");
  const unchecked = lines.filter((l) => l.state === "unknown");
  const done = lines.filter((l) => l.state === "done" || l.state === "info");
  return (
    <section className="bare ks-needs" aria-labelledby="needs-title">
      <h2 className="ks-label" id="needs-title">
        Needs your decision
        {total > 0 && <span className="ks-badge">{total}</span>}
      </h2>
      {decisionsError && <p role="alert">{decisionsError} The close's decisions could not be read; this list may be incomplete.</p>}
      <div className="ks-tasks">
        {shown.map((need, i) => (
          <div className="ks-task" key={need.key}>
            <span className="ks-numeral">{i + 1}</span>
            <div>
              <b>{need.title}</b>
              <p>{need.detail}</p>
            </div>
            {need.act ? (
              <button type="button" className={i === 0 ? "primary" : undefined} onClick={need.act}>
                {need.action}
              </button>
            ) : need.to ? (
              <Link className={i === 0 ? "button primary" : "button"} to={need.to}>
                {need.action}
              </Link>
            ) : (
              <span />
            )}
          </div>
        ))}
        {needs.length === 0 && !decisionsError && (
          <p className="ks-calm">
            <Icon name="check" />
            {preparing ? "Nothing needs you now. The agent is preparing, and brings each decision here when it is ready." : "Nothing needs you now."}
          </p>
        )}
        {rest.length > 0 && (
          <button type="button" className="ks-fold" aria-expanded={all} onClick={() => setAll(!all)}>
            <Icon name="arrow" />
            {all ? "Show the first three" : `${rest.reduce((n, need) => n + need.count, 0)} more · ${list([...new Set(rest.map((need) => need.action.toLowerCase()))])}`}
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

// Rail is the same three panels on every tab: a few figures found nowhere else on the page, the agent in one line with
// its activity folded, and more on this close.
function Rail({ close, status, papers, brief, base }: { close: Close; status: Status | undefined; papers: Paper[]; brief: Brief | undefined; base: string }) {
  const dates = useDates();
  const effort = useQuery(workspaceQuery<Effort>(`/${close.id}/effort`));
  const [history, setHistory] = useState(false);
  const [all, setAll] = useState<Status["activity"]>();
  const [error, setError] = useState("");
  const current = papers.filter((p) => !papers.some((q) => q.paper.revises === p.id));
  const approved = current.filter((p) => p.review_state === "approved").length;
  const coverage = status?.progress.coverage?.total ? status.progress.coverage : undefined;
  const working = status?.goal?.state === "active" ? status.progress.items.filter((i) => i.status === "working").length : 0;
  const figures: [string, string, string?][] = [
    ...(current.length ? ([[`${approved} of ${current.length}`, "drafts approved"]] as [string, string][]) : []),
    ...(coverage ? ([[`${coverage.total - coverage.open} of ${coverage.total}`, "accounts covered", coverage.open ? "att" : ""]] as [string, string, string][]) : []),
    ...(working ? ([[String(working), working === 1 ? "document the agent is preparing" : "documents the agent is preparing"]] as [string, string][]) : []),
    ...(effort.data && effort.data.tracked_seconds > 0 ? ([[`${Math.max(1, Math.round(effort.data.tracked_seconds / 60))} min`, "time spent on this close"]] as [string, string][]) : []),
  ];
  const latest = status?.activity[0];
  const total = status?.activity_total ?? 0;
  const line = status?.goal ? headline(status.goal, status.progress, goalNeeds(status)) : status?.signed_off ? "Signed off; nothing left to prepare." : status ? "Not handed over yet." : "";
  // The sign-off line's own words, without repeating the row's name.
  const signOff = brief?.lines.find((l) => l.area === "sign-off")?.text.replace(/^Sign-off\s+/i, "");
  const next = status?.progress.signoff?.next_close;
  return (
    <aside className="ks-rail" aria-label="About this close">
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
      <section className="ks-panel" aria-labelledby="more-title">
        <h2 className="ks-label" id="more-title">
          More on this close
        </h2>
        <nav className="ks-links" aria-labelledby="more-title">
          <Link to={`${base}?tab=signoff`}>
            <span>Sign-off checks</span>
            {signOff && <small>{signOff[0].toUpperCase() + signOff.slice(1)}</small>}
            <Icon name="chevron" size={14} />
          </Link>
          <Link to={`${base}#agent-details`}>
            <span>The agent's plan, documents and accounts</span>
            <Icon name="chevron" size={14} />
          </Link>
          {next && (
            <Link to={`/dashboard/closes/${next}`}>
              <span>Next month's close</span>
              <Icon name="chevron" size={14} />
            </Link>
          )}
        </nav>
      </section>
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
        <NeedsYou needs={[...handing, ...needs]} teammates={teammates} brief={brief} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={preparing(status)} />
        {status ? <AgentWork status={status} papers={papers} base={base} /> : error ? <p role="alert">{error}</p> : null}
        <AgentDetails closeId={close.id} onPrepared={onPrepared} />
      </div>
      <Rail close={close} status={status} papers={papers} brief={brief} base={base} />
    </div>
  );
}
