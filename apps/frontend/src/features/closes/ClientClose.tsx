import { clientPage } from "../../data/clients";
import { closeTab, closeTabs, type CloseTab } from "../../data/closeTabs";
import { monthName, useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { CloseLede, signOffAvailable, useBrief } from "./Stands";
import { EffortTracker, type Activity } from "./Effort";
import { Books, BooksLede } from "./Books";
import { Workpapers, WorkpapersLede } from "./Workpapers";
import { Documents, DocumentsLede } from "./Documents";
import { RequestsLede, RequestsTab } from "./RequestsTab";
import { ReportsLede, ReportsTab } from "./ReportsTab";
import { CashLede, CashTab } from "./CashTab";
import { SignOffLede, SignOffTab } from "./SignOffTab";
import { SettingsLede, SettingsTab } from "./SettingsTab";
import { AgentChip, CloseMenu, countsOf, needsFor, Overview, useDecisions, useGoalStatus } from "./Overview";
import { useClose, type Profile } from "./WorkpaperView";

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

// Each tab's own actions and exception paths, at the top of the close's ⋯ menu.
const tabMenus = (base: string): Partial<Record<Tab, { title: string; items: { label: string; to: string }[] }>> => ({
  settings: {
    title: "Settings",
    items: [
      { label: "Replay the confirmed readings", to: `${base}?tab=settings&view=replay` },
      { label: "Learn a procedure from evidence", to: `${base}?tab=settings&view=procedures` },
      { label: "Delete this close", to: `${base}?tab=settings&view=retention` },
    ],
  },
  signoff: {
    title: "Sign-off",
    items: [
      { label: "Download the review package", to: `${base}?tab=signoff&view=package` },
      { label: "Change the scope", to: `${base}?tab=signoff&view=readiness` },
      { label: "Record time off-screen", to: `${base}?tab=signoff&view=effort` },
      { label: "Rehearse the ledger", to: `${base}?tab=signoff&view=rehearsal` },
    ],
  },
  cash: {
    title: "Cash plan",
    items: [
      { label: "Add a schedule", to: `${base}?tab=cash&view=plan#cash-schedules` },
      { label: "Try a what-if", to: `${base}?tab=cash&view=plan#cash-scenarios` },
      { label: "Review this plan", to: `${base}?tab=cash&view=plan#cash-reviews` },
    ],
  },
  reports: {
    title: "Reports",
    items: [
      { label: "Publish the pack", to: `${base}?tab=reports&view=publish` },
      { label: "Download a published PDF or CSV", to: `${base}?tab=reports&view=publish` },
      { label: "Import a budget", to: `${base}?tab=reports&view=budget` },
      { label: "Read the full pack", to: `${base}?tab=reports&view=pack` },
    ],
  },
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
            <Link className={signOffFirst ? "button primary" : "button"} to={`${base}?tab=signoff&view=readiness#signoff-form`}>
              Sign off
            </Link>
          )}
          <CloseMenu close={close} status={goalRead.data} onPlanSaved={() => void reload()} tab={tabMenus(base)[tab]} />
        </div>
      </div>
      {refreshError && <p role="alert">{refreshError} Showing the last read.</p>}
      {tab === "books" ? <BooksLede close={close} base={base} /> : tab === "workpapers" ? <WorkpapersLede papers={papers} more={more} /> : tab === "requests" ? <RequestsLede closeId={close.id} /> : tab === "reports" ? <ReportsLede closeId={close.id} period={close.period} /> : tab === "cash" ? <CashLede closeId={close.id} base={base} /> : tab === "signoff" ? <SignOffLede close={close} /> : tab === "settings" ? <SettingsLede close={close} /> : tab === "evidence" ? <DocumentsLede closeId={close.id} base={base} waiting={needs.filter((need) => need.tab === "evidence").reduce((n, need) => n + need.count, 0)} /> : <CloseLede close={close} counts={counts} />}
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
      {tab === "reports" && <ReportsTab close={close} papers={papers} needs={needs.filter((need) => need.tab === "reports")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} />}
      {tab === "cash" && <CashTab close={close} papers={papers} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} />}
      {tab === "signoff" && <SignOffTab close={close} papers={papers} needs={needs.filter((need) => need.tab === "signoff")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} />}
      {tab === "settings" && <SettingsTab close={close} papers={papers} needs={needs.filter((need) => need.tab === "settings")} brief={brief} briefRead={briefRead} decisionsError={decisionsError} status={goalRead.data} base={base} onApplied={() => void reload()} onDeleted={setDeleted} />}
    </>
  );
}
