import { clientPage } from "../../data/clients";
import { closeTab, closeTabs, type CloseTab } from "../../data/closeTabs";
import { monthName, useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { CloseLede, signOffAvailable, useBrief } from "./Stands";
import { CashPlan } from "./CashPlan";
import { Reports } from "./Reports";
import { Budget } from "./Budget";
import { RetentionControls } from "./Enterprise";
import { ProposedStatePreview } from "./Posting";
import { Procedures } from "./Procedures";
import { Memory } from "./Memory";
import { Readiness } from "./Readiness";
import { OpeningBaseline } from "./OpeningBaseline";
import { TakeoverSetup } from "./TakeoverSetup";
import { Replay } from "./Replay";
import { EffortCard, EffortTracker, type Activity } from "./Effort";
import { Books, BooksLede } from "./Books";
import { Workpapers, WorkpapersLede } from "./Workpapers";
import { Documents, DocumentsLede } from "./Documents";
import { RequestsLede, RequestsTab } from "./RequestsTab";
import { AgentChip, CloseMenu, countsOf, needsFor, Overview, useDecisions, useGoalStatus } from "./Overview";
import { workspaceRequest } from "./workspace";
import { useClose, type Close, type Profile } from "./WorkpaperView";

// Overview holds what needs a person; Books holds what the ledger shows; the rest is where each kind of work lives.
const tabs = closeTabs;
type Tab = CloseTab;
const tabActivity: Record<Tab, Activity> = { overview: "prepare", books: "review", workpapers: "prepare", evidence: "evidence", requests: "requests", reports: "review", cash: "review", signoff: "signoff", settings: "configuration" };



function profileLabel(p: Profile) {
  if (p.state === "conflict") return "Connected ledgers disagree on currency or fiscal year; disconnect the wrong one";
  if (p.state === "differs")
    return p.ledger?.conflict
      ? `${p.currency} confirmed, but the connected ledgers disagree with each other: disconnect the wrong one`
      : `${p.currency} confirmed, but the ledger reports ${[p.ledger?.currency, p.ledger?.fiscal_year_start ? "another fiscal year" : ""].filter(Boolean).join(" and ")}: correct the client's profile or its connection`;
  if (p.state === "unknown") return "USD assumed until the ledger's currency is read: reconnect QuickBooks, or capture a Xero snapshot";
  return `${p.currency} · fiscal year starts ${new Date(2000, p.fiscal_year_start - 1).toLocaleString("en-US", { month: "long" })}`;
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

// Each tab's own actions and exception paths, at the top of the close's ⋯ menu.
const tabMenus = (base: string): Partial<Record<Tab, { title: string; items: { label: string; to: string }[] }>> => ({
  requests: {
    title: "Requests",
    items: [
      { label: "New request", to: `${base}?tab=requests&view=draft` },
      { label: "Review email replies", to: `${base}?tab=requests&view=replies` },
    ],
  },
  evidence: {
    title: "Documents",
    items: [
      { label: "Upload a document", to: `${base}?tab=evidence&view=add` },
      { label: "Forward documents by email", to: `${base}?tab=evidence&view=add` },
      { label: "Replace or compare a document", to: `${base}?tab=evidence&view=add` },
      { label: "Run saved preparation instructions", to: `${base}?tab=evidence&view=instructions` },
    ],
  },
  workpapers: {
    title: "Workpapers",
    items: [
      { label: "Refresh recurring schedules", to: `${base}?tab=workpapers&view=recurring` },
      { label: "Refresh recurring accruals", to: `${base}?tab=workpapers&view=accruals` },
      { label: "Carry work into the next month", to: `${base}?tab=workpapers&view=carry` },
    ],
  },
  books: {
    title: "Books",
    items: [
      { label: "Capture the trial balance again", to: `${base}?tab=books&view=ledger` },
      { label: "Client questions from the exceptions", to: `${base}?tab=books&view=exceptions` },
      { label: "Correct a transaction in QuickBooks", to: `${base}?tab=books&view=corrections` },
      { label: "Apply a payment or vendor credit", to: `${base}?tab=books&view=corrections` },
    ],
  },
});

export default function ClientClose() {
  const { id } = useParams();
  const location = useLocation();
  const [params] = useSearchParams();
  const { close, papers, more, error, refreshError, loading, reload } = useClose(id);
  const briefRead = useBrief(id ?? "");
  const brief = briefRead.data;
  const decisionsRead = useDecisions(id ?? "");
  const goalRead = useGoalStatus(id ?? "");
  const dates = useDates();
  const [deleted, setDeleted] = useState("");
  const tab: Tab = closeTab(params.get("tab"));
  // A link to a section scrolls to it once it is there: a section can mount after the month's data loads, so the
  // target is looked for each frame for a few seconds.
  useEffect(() => {
    if (!location.hash) return;
    const target = location.hash.slice(1);
    const deadline = Date.now() + 5000;
    let frame = 0;
    const find = () => {
      const section = document.getElementById(target);
      if (section) section.scrollIntoView();
      else if (Date.now() < deadline) frame = requestAnimationFrame(find);
    };
    frame = requestAnimationFrame(find);
    return () => cancelAnimationFrame(frame);
  }, [tab, location.hash]);
  if (deleted)
    return (
      <p role="status">
        Close removed. Deletion receipt #{deleted}. <Link to="/dashboard/clients">Return to client closes</Link>
      </p>
    );
  if (loading && !close) return <p role="status">Opening close…</p>;
  if (error || !close) return <p role="alert">{error || "Close not found."}</p>;
  const base = `/dashboard/closes/${close.id}`;
  // What needs the person, counted once for the sentence, the tabs' badges and Overview's list. Decisions that could not
  // be read are said so, never counted as none.
  const { needs, teammates } = needsFor(decisionsRead.data?.events, brief, papers, base);
  const counts = countsOf(needs, goalRead.data, !decisionsRead.isError ? "" : decisionsRead.data ? `decisions not refreshed since ${dates.time(decisionsRead.dataUpdatedAt)}` : "decisions not read");
  const decisionsError = decisionsRead.isError ? decisionsRead.error.message || "This close's decisions are unavailable." : "";
  // Sign off waits until readiness says the close is ready, read without error, for a reader whose role may sign off: a
  // status that could not be refreshed offers nothing on it. It is the page's one primary only when nothing else needs
  // the person; otherwise the first need's action is.
  const signOffBlocked = !!briefRead.error || !signOffAvailable(brief) || !close.can_sign_off;
  const signOffFirst = counts.decisions + counts.blockers === 0 && !counts.handOver;
  return (
    <>
      <EffortTracker closeId={close.id} activity={tabActivity[tab]} />
      <div className="ks-crumbs">
        <Link to="/dashboard/clients">Clients</Link>
        <span>/</span>
        <Link to={clientPage(close.entity)}>{close.entity}</Link>
        <span>/</span>
        <b>{monthName(close.period)}</b>
        <div className="ks-tools">
          <AgentChip status={goalRead.data} />
          {signOffBlocked ? (
            <button type="button" disabled title={!close.can_sign_off ? "Your role cannot sign off; a reviewer can" : "Not ready yet; the Sign-off tab shows what blocks it"}>
              Sign off
            </button>
          ) : (
            <Link className={signOffFirst ? "button primary" : "button"} to={`${base}?tab=signoff`}>
              Sign off
            </Link>
          )}
          <CloseMenu close={close} status={goalRead.data} onPlanSaved={() => void reload()} tab={tabMenus(base)[tab]} />
        </div>
      </div>
      {refreshError && <p role="alert">{refreshError} Showing the last read.</p>}
      {tab === "books" ? <BooksLede close={close} base={base} /> : tab === "workpapers" ? <WorkpapersLede papers={papers} more={more} /> : tab === "requests" ? <RequestsLede closeId={close.id} /> : tab === "evidence" ? <DocumentsLede closeId={close.id} base={base} waiting={needs.filter((need) => need.tab === "evidence").reduce((n, need) => n + need.count, 0)} /> : <CloseLede close={close} counts={counts} />}
      {close.profile && (close.profile.state === "conflict" || close.profile.state === "differs" || close.profile.state === "unknown") && (
        <p className="ks-lede-notes">
          <span className="chip warn" role={close.profile.state === "unknown" ? undefined : "alert"} title={close.profile.state === "differs" ? "The client's confirmed profile" : "Read from the connected ledger"}>
            {profileLabel(close.profile)}
          </span>
        </p>
      )}
      <nav className="ks-tabs" aria-label="Close sections">
        {tabs.map(([key, label]) => (
          <Link key={key} to={`${base}?tab=${key}`} aria-current={tab === key ? "page" : undefined}>
            {label}
            {!!counts.tabs[key] && (
              <span className="ks-badge">
                <span className="visually-hidden">, </span>
                {counts.tabs[key]}
                <span className="visually-hidden">{counts.tabs[key] === 1 ? " decision" : " decisions"}</span>
              </span>
            )}
          </Link>
        ))}
      </nav>
      {tab === "overview" && <Overview close={close} papers={papers} needs={needs} teammates={teammates} brief={brief} briefRead={briefRead} decisionsError={decisionsError} base={base} onPrepared={() => void reload()} />}
      {tab === "books" && <Books close={close} papers={papers} needs={needs.filter((need) => need.tab === "books")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} />}
      {tab === "workpapers" && <Workpapers close={close} papers={papers} more={more} needs={needs.filter((need) => need.tab === "workpapers")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} statusError={!goalRead.isError ? "" : goalRead.data ? "stale" : "failed"} base={base} onPrepared={() => void reload()} />}
      {tab === "evidence" && <Documents close={close} needs={needs.filter((need) => need.tab === "evidence")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} onChange={() => void reload()} />}
      {tab === "requests" && <RequestsTab close={close} papers={papers} needs={needs.filter((need) => need.tab === "requests")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} onChange={() => void reload()} />}
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
          <Procedures key={`procedures-${close.id}`} closeId={close.id} entity={close.entity} period={close.period} onApplied={() => void reload()} />
          <Replay key={`replay-${close.id}`} closeId={close.id} />
          <RetentionControls close={close} onDeleted={setDeleted} />
        </div>
      )}
    </>
  );
}
