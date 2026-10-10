import { closeTab, closeTabs, type CloseTab } from "../../data/closeTabs";
import { monthName, useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { Icon } from "@close/ui/components/Icon";
import { IconTile } from "@close/ui/components/IconTile";
import { Segmented } from "@close/ui/components/Segmented";
import { StatGroup, StatTile } from "@close/ui/components/StatTile";
import { BillCorrection } from "./BillCorrection";
import { CreditApplications } from "./CreditApplications";
import { CloseLede, CloseTasks, signOffAvailable, useBrief } from "./Stands";
import { CarryForward } from "./CarryForward";
import { Coordinator } from "./Coordinator";
import { GoalPanel } from "./Goal";
import { EmailReplies } from "./EmailReplies";
import { Aging } from "./Aging";
import { CashPlan } from "./CashPlan";
import { OpenItems } from "./OpenItems";
import { Matching } from "./Matching";
import { Exceptions } from "./Exceptions";
import { Flux } from "./Flux";
import { Reports } from "./Reports";
import { Budget } from "./Budget";
import { TieOut } from "./TieOut";
import { RetentionControls } from "./Enterprise";
import { Evidence, PreparationJobs } from "./Evidence";
import { ProposedStatePreview } from "./Posting";
import { Procedures } from "./Procedures";
import { Memory } from "./Memory";
import { ProductionAccess } from "./ProductionAccess";
import { QuickBooks } from "./QuickBooks";
import { Xero } from "./Xero";
import { HistoricalExports } from "./HistoricalExports";
import { Stripe } from "./Stripe";
import { Readiness } from "./Readiness";
import { OpeningBaseline } from "./OpeningBaseline";
import { TakeoverSetup } from "./TakeoverSetup";
import { Recurring } from "./Recurring";
import { Replay } from "./Replay";
import { RecurringAccruals } from "./RecurringAccruals";
import { Requests } from "./Requests";
import { EffortCard, EffortTracker, type Activity } from "./Effort";
import { Work } from "./Work";
import { workspaceRequest } from "./workspace";
import { needsAttention, paperLabel, readyForReview, ReviewPill, useClose, type Close, type Paper, type Profile } from "./WorkpaperView";
import { EntityCell, RowList } from "@close/ui/components/RowList";

// Overview holds what needs a person; Books holds what the ledger shows; the rest is where each kind of work lives.
const tabs = closeTabs;
type Tab = CloseTab;
const tabActivity: Record<Tab, Activity> = { overview: "prepare", books: "review", workpapers: "prepare", evidence: "evidence", requests: "requests", reports: "review", cash: "review", signoff: "signoff", settings: "configuration" };
const filters: Record<string, [string, (paper: Paper) => boolean]> = {
  all: ["All drafts", () => true],
  ready: ["Ready for review", readyForReview],
  attention: ["Needs attention", needsAttention],
  approved: ["Approved", (paper) => paper.review_state === "approved"],
};


const basisLabels = { cash: "Cash basis", modified_cash: "Modified cash basis", accrual: "Accrual basis" };

function profileLabel(p: Profile) {
  if (p.state === "conflict") return "Connected ledgers disagree on currency or fiscal year; disconnect the wrong one";
  if (p.state === "unknown") return "USD assumed until the ledger's currency is read: reconnect QuickBooks, or capture a Xero snapshot";
  return `${p.currency} · fiscal year starts ${new Date(2000, p.fiscal_year_start - 1).toLocaleString("en-US", { month: "long" })}`;
}

// The preparation forms, for work the agent did not prepare from evidence, behind one menu.
function PrepareActions({ close }: { close: Close }) {
  const query = `close=${close.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`;
  const accrualWork = close.basis !== "cash" && close.basis !== "modified_cash";
  const forms: [string, string][] = [
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
  return (
    <details className="prepare-menu">
      <summary className="button">Prepare by hand</summary>
      <div>
        {forms.map(([label, to]) => (
          <Link key={to} to={to}>
            {label}
          </Link>
        ))}
      </div>
    </details>
  );
}

function Package({ close }: { close: Close }) {
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  async function download(kind: "export" | "binder" | "year-end") {
    setBusy(true);
    setStatus("");
    try {
      const blob = await workspaceRequest(`/${close.id}/${kind}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      const year = close.period.slice(0, 4);
      link.download = kind === "year-end" ? `year-end-handoff-${close.id}-${year}.zip` : `${kind === "binder" ? "accountant-binder" : "close-review"}-${close.id}-${close.period}.zip`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setStatus(
        kind === "year-end"
          ? `Year-end handoff ready for January through ${close.period}. Open handoff.html for the summary and open-questions.csv for what still needs an answer.`
          : "Package ready. Open review.html for the summary and checksums.sha256 to verify its files.",
      );
    } catch (e) {
      setStatus(e instanceof Error ? e.message : "Export failed. Saved work has not changed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h2>Take the close into review</h2>
      <p>Download every saved version, CSV tables, source references, unresolved items and stored sandbox receipts. The package records the supported scope and sign-off history; unlisted work is not certified.</p>
      <div className="title-actions">
        <button disabled={busy} onClick={() => void download("export")}>
          {busy ? "Preparing package…" : "Download close review package"}
        </button>
        <button disabled={busy} onClick={() => void download("binder")}>
          Download signed accountant binder
        </button>
        <button disabled={busy} onClick={() => void download("year-end")}>
          Download year-end handoff
        </button>
      </div>
      <p>The binder is available only while the current supported scope remains signed off with no blockers. A new source or unresolved action requires fresh review.</p>
      <p>The year-end handoff collects this client's approved schedules, roll-forwards, reconciled balances, adjustments, posting records and open questions from January through this month, for the firm's tax and audit work.</p>
      <p role="status">{status}</p>
    </section>
  );
}

export default function ClientClose() {
  const { id } = useParams();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const { close, papers, more, error, refreshError, loading, reload } = useClose(id);
  const briefRead = useBrief(id ?? "");
  const brief = briefRead.data;
  const dates = useDates();
  const [deleted, setDeleted] = useState("");
  const tab: Tab = closeTab(params.get("tab"));
  const filter = filters[params.get("filter") ?? ""] ? params.get("filter")! : "all";
  useEffect(() => {
    if (location.hash) requestAnimationFrame(() => document.getElementById(location.hash.slice(1))?.scrollIntoView());
  }, [tab, location.hash]);
  if (deleted)
    return (
      <p role="status">
        Close removed. Deletion receipt #{deleted}. <Link to="/dashboard/clients">Return to client closes</Link>
      </p>
    );
  if (loading && !close) return <p role="status">Opening close…</p>;
  if (error || !close) return <p role="alert">{error || "Close not found."}</p>;
  const ready = papers.filter(readyForReview);
  const attention = papers.filter(needsAttention);
  const approved = papers.filter((paper) => paper.review_state === "approved");
  const shown = papers.filter(filters[filter][1]);
  const base = `/dashboard/closes/${close.id}`;
  const latest = more ? " among the latest 100" : "";
  // Sign off is the page's one action, and waits until readiness says the close is ready, read without error: a
  // status that could not be refreshed offers nothing on it.
  const signOffBlocked = !!briefRead.error || !signOffAvailable(brief);
  return (
    <>
      <EffortTracker closeId={close.id} activity={tabActivity[tab]} />
      <div className="ks-crumbs">
        <Link to="/dashboard/clients">Clients</Link>
        <span>/</span>
        <Link to={`/dashboard/clients?q=${encodeURIComponent(close.entity)}`}>{close.entity}</Link>
        <span>/</span>
        <b>{monthName(close.period)}</b>
        <div className="ks-tools">
          <PrepareActions close={close} />
          {signOffBlocked ? (
            <button type="button" className="primary" disabled title="Not ready yet; the Sign-off tab shows what blocks it">
              Sign off
            </button>
          ) : (
            <Link className="button primary" to={`${base}?tab=signoff`}>
              Sign off
            </Link>
          )}
        </div>
      </div>
      {refreshError && <p role="alert">{refreshError} Showing the last read.</p>}
      <CloseLede close={close} onPlanSaved={() => void reload()} />
      <p className="ks-lede-notes">
        {close.basis && <span className="chip" title="Set under Settings → Approved procedures and client overrides">{basisLabels[close.basis]}</span>}
        {close.profile && close.profile.state !== "none" && <span className={close.profile.state === "conflict" || close.profile.state === "unknown" ? "chip warn" : "chip"} role={close.profile.state === "conflict" ? "alert" : undefined} title="Read from the connected ledger">{profileLabel(close.profile)}</span>}
      </p>
      <nav className="ks-tabs" aria-label="Close sections">
        {tabs.map(([key, label]) => (
          <Link key={key} to={`${base}?tab=${key}`} aria-current={tab === key ? "page" : undefined}>
            {label}
          </Link>
        ))}
      </nav>
      {tab === "overview" && (
        <div className="stack">
          <CloseTasks key={`stands-${close.id}-${papers.map((paper) => paper.id + paper.review_state).join()}`} closeId={close.id} base={base} />
          <GoalPanel key={`goal-${close.id}`} closeId={close.id} />
          <Coordinator key={`coordinator-${close.id}`} closeId={close.id} />
          <Work key={`work-${close.id}`} closeId={close.id} onPrepared={() => void reload()} />
        </div>
      )}
      {tab === "books" && (
        <div className="stack">
          <TieOut key={`tie-out-${close.id}`} closeId={close.id} />
          <Exceptions key={`exceptions-${close.id}`} closeId={close.id} />
          <Flux key={`flux-${close.id}`} closeId={close.id} />
          <Aging key={`aging-${close.id}`} closeId={close.id} />
          <OpenItems key={`open-items-${close.id}`} closeId={close.id} />
          <Matching key={`matching-${close.id}`} closeId={close.id} />
          <BillCorrection closeId={close.id} />
          <BillCorrection closeId={close.id} kind="purchases" />
          <BillCorrection closeId={close.id} kind="deposits" />
          <CreditApplications closeId={close.id} />
        </div>
      )}
      {tab === "workpapers" && (
        <div className="stack">
      <StatGroup>
        <StatTile icon="review" label="Ready for review" value={ready.length} caption={`Current drafts with evidence and no blockers${latest}`} to={`${base}?tab=workpapers&filter=ready`} />
        <StatTile icon="blocked" tone="red" label="Needs attention" value={attention.length} caption={`Blockers, changes requested or evidence changed${latest}`} to={`${base}?tab=workpapers&filter=attention`} />
        <StatTile icon="decision" tone="teal" label="Approved" value={approved.length} caption="Approval is not posting or sign-off" to={`${base}?tab=workpapers&filter=approved`} />
        <StatTile icon="file" tone="gray" label="Saved drafts" value={papers.length} caption={more ? "Latest 100 shown" : "All versions in this close"} to={`${base}?tab=workpapers`} />
      </StatGroup>
          <div className="toolbar">
            <Segmented
              label="Filter drafts"
              items={Object.entries(filters).map(([key, [label, test]]) => ({ key, label, count: papers.filter(test).length }))}
              value={filter}
              onChange={(key) => setParams({ tab: "workpapers", filter: key })}
            />
          </div>
          <RowList columns={["Workpaper", "Review", "Prepared", ""]}>
            {shown.length === 0 && <p className="empty">No drafts match this view. Prepare work from evidence or change the filter.</p>}
            {shown.map((paper) => (
              <Link className="row" key={paper.id} to={`${base}/workpapers/${paper.id}`}>
                <EntityCell
                  leading={<IconTile icon="file" tone="gray" />}
                  title={paperLabel(paper)}
                  detail={`Draft #${paper.id}${paper.paper.revises ? ` · revises #${paper.paper.revises}` : ""}${(paper.paper.blockers?.length ?? 0) > 0 ? ` · ${paper.paper.blockers!.length} blocker${paper.paper.blockers!.length === 1 ? "" : "s"}` : ""}`}
                />
                <span>
                  <ReviewPill state={paper.review_state} />
                </span>
                <small>{dates.dateTime(paper.created_at)}</small>
                <Icon name="arrow" />
              </Link>
            ))}
          </RowList>
          {more && <p className="note">Showing the 100 most recent drafts. Older records remain saved.</p>}
          <Recurring key={`recurring-${close.id}`} closeId={close.id} currency={close.profile?.currency ?? "USD"} onPrepared={() => void reload()} />
          <RecurringAccruals closeId={close.id} currency={close.profile?.currency ?? "USD"} onPrepared={() => void reload()} />
          <CarryForward key={close.id} closeId={close.id} />
        </div>
      )}
      {tab === "evidence" && (
        <div className="stack">
          <Evidence key={`evidence-${close.id}`} closeId={close.id} onReplace={() => void reload()} />
          <QuickBooks key={`qbo-${close.id}`} closeId={close.id} entity={close.entity} />
          <Xero key={`xero-${close.id}`} closeId={close.id} entity={close.entity} />
          <HistoricalExports key={`history-${close.id}`} closeId={close.id} />
          <Stripe key={`stripe-${close.id}`} closeId={close.id} entity={close.entity} />
          <PreparationJobs key={`jobs-${close.id}`} closeId={close.id} onComplete={() => void reload()} />
        </div>
      )}
      {tab === "requests" && (
        <div className="stack">
          <Requests closeId={close.id} papers={papers} />
          <EmailReplies closeId={close.id} onReleased={() => void reload()} />
        </div>
      )}
      {tab === "reports" && (
        <>
          <Reports key={`reports-${close.id}`} closeId={close.id} />
          <Budget key={`budget-${close.id}`} closeId={close.id} />
        </>
      )}
      {tab === "cash" && <CashPlan key={`cash-${close.id}`} closeId={close.id} />}
      {tab === "signoff" && (
        <div className="stack">
          <TakeoverSetup key={`setup-${close.id}`} closeId={close.id} />
          <OpeningBaseline key={`opening-${close.id}`} closeId={close.id} />
          <Readiness key={`readiness-${close.id}`} closeId={close.id} papers={papers} />
          <ProposedStatePreview closeId={close.id} />
          <EffortCard closeId={close.id} />
          <Package close={close} />
        </div>
      )}
      {tab === "settings" && (
        <div className="stack">
          <Memory key={`memory-${close.id}`} closeId={close.id} />
          <Procedures key={`procedures-${close.id}`} closeId={close.id} entity={close.entity} onApplied={() => void reload()} />
          <ProductionAccess closeId={close.id} entity={close.entity} currency={close.profile?.currency ?? "USD"} />
          <Replay key={`replay-${close.id}`} closeId={close.id} />
          <RetentionControls close={close} onDeleted={setDeleted} />
        </div>
      )}
    </>
  );
}
