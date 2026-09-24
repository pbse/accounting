import { useEffect, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router";
import { Avatar, Icon, type IconName } from "../dashboard/Icon";
import { BillCorrection } from "./BillCorrection";
import { CarryForward } from "./CarryForward";
import { Coordinator } from "./Coordinator";
import { EmailReplies } from "./EmailReplies";
import { Exceptions } from "./Exceptions";
import { RetentionControls } from "./Enterprise";
import { Evidence, PreparationJobs } from "./Evidence";
import { ProposedStatePreview } from "./Posting";
import { Procedures } from "./Procedures";
import { ProductionAccess } from "./ProductionAccess";
import { QuickBooks } from "./QuickBooks";
import { Readiness } from "./Readiness";
import { Recurring } from "./Recurring";
import { RecurringAccruals } from "./RecurringAccruals";
import { Requests } from "./Requests";
import { EffortCard, EffortTracker, type Activity } from "./Effort";
import { PlanChips, PlanningControls } from "./Planning";
import { Work } from "./Work";
import { workspaceRequest } from "./workspace";
import { needsAttention, paperLabels, readyForReview, ReviewPill, useClose, type Close, type Paper } from "./WorkpaperView";

const tabs = [
  ["overview", "Overview"],
  ["workpapers", "Workpapers"],
  ["evidence", "Evidence"],
  ["requests", "Requests"],
  ["signoff", "Close & sign-off"],
  ["settings", "Settings"],
] as const;
type Tab = (typeof tabs)[number][0];
const tabActivity: Record<Tab, Activity> = { overview: "prepare", workpapers: "prepare", evidence: "evidence", requests: "requests", signoff: "signoff", settings: "configuration" };
const filters: Record<string, [string, (paper: Paper) => boolean]> = {
  all: ["All drafts", () => true],
  ready: ["Ready for review", readyForReview],
  attention: ["Needs attention", needsAttention],
  approved: ["Approved", (paper) => paper.review_state === "approved"],
};

function Kpi({ icon, tone, label, value, caption, to }: { icon: IconName; tone: string; label: string; value: number; caption: string; to: string }) {
  return (
    <Link className="kpi" to={to}>
      <span className="kpi-head">
        <span className={`kpi-icon ${tone}`}>
          <Icon name={icon} size={16} />
        </span>
        {label}
      </span>
      <span className="kpi-body">
        <span className="kpi-value">{value}</span>
        <span className="kpi-caption">{caption}</span>
      </span>
    </Link>
  );
}

const basisLabels = { cash: "Cash basis", modified_cash: "Modified cash basis", accrual: "Accrual basis" };

function PrepareActions({ close }: { close: Close }) {
  const query = `close=${close.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`;
  const accrualWork = close.basis !== "cash" && close.basis !== "modified_cash";
  return (
    <div className="title-actions">
      {accrualWork && (
        <>
          <Link className="button primary" to={`/dashboard/prepaids/new?${query}`}>
            Prepare a prepaid
          </Link>
          <Link className="button" to={`/dashboard/accruals/new?${query}`}>
            Accrued expense
          </Link>
        </>
      )}
      <Link className="button" to={`/dashboard/loans/new?${query}`}>
        Loan schedule
      </Link>
      <Link className="button" to={`/dashboard/reconciliations/new?${query}`}>
        Statement reconciliation
      </Link>
      <Link className="button" to={`/dashboard/settlements/new?${query}`}>
        Stripe payouts
      </Link>
    </div>
  );
}

function Package({ close }: { close: Close }) {
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  async function download(kind: "export" | "binder") {
    setBusy(true);
    setStatus("");
    try {
      const blob = await workspaceRequest(`/${close.id}/${kind}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `${kind === "binder" ? "accountant-binder" : "close-review"}-${close.id}-${close.period}.zip`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setStatus("Package ready. Open review.html for the summary and checksums.sha256 to verify its files.");
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
      </div>
      <p>The binder is available only while the current supported scope remains signed off with no blockers. A new source or unresolved action requires fresh review.</p>
      <p role="status">{status}</p>
    </section>
  );
}

export default function ClientClose() {
  const { id } = useParams();
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const { close, papers, more, error, loading, reload } = useClose(id);
  const [deleted, setDeleted] = useState("");
  const tab: Tab = tabs.some(([key]) => key === params.get("tab")) ? (params.get("tab") as Tab) : "overview";
  const filter = filters[params.get("filter") ?? ""] ? params.get("filter")! : "all";
  useEffect(() => {
    if (location.hash) requestAnimationFrame(() => document.getElementById(location.hash.slice(1))?.scrollIntoView());
  }, [tab, location.hash]);
  if (deleted)
    return (
      <p role="status">
        Close removed. Deletion receipt #{deleted}. <Link to="/dashboard/closes">Return to client closes</Link>
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
  return (
    <>
      <EffortTracker closeId={close.id} activity={tabActivity[tab]} />
      <Link className="back-link" to="/dashboard/closes">
        <Icon name="back" size={15} /> Client closes
      </Link>
      <div className="page-title">
        <div>
          <Avatar label={close.entity} />
          <span>
            <h1>{close.entity}</h1>
            <p>
              Close month {close.period} · prepared work stays a draft until an accountant approves it <PlanChips plan={close} />
              {close.basis && <span className="chip" style={{ marginLeft: "0.5rem" }} title="Set under Settings → Approved procedures and client overrides">{basisLabels[close.basis]} · {close.basis_scope === "client" ? "client override" : "firm default"}</span>}
            </p>
          </span>
        </div>
        <PrepareActions close={close} />
      </div>
      <PlanningControls key={`${close.due_on}-${close.priority}-${close.owned_by_me}-${close.assigned}`} closeId={close.id} plan={close} onSaved={() => void reload()} />
      <div className="kpis">
        <Kpi icon="review" tone="" label="Ready for review" value={ready.length} caption={`Current drafts with evidence and no blockers${latest}`} to={`${base}?tab=workpapers&filter=ready`} />
        <Kpi icon="blocked" tone="red" label="Needs attention" value={attention.length} caption={`Blockers, changes requested or evidence changed${latest}`} to={`${base}?tab=workpapers&filter=attention`} />
        <Kpi icon="decision" tone="green" label="Approved" value={approved.length} caption="Approval is not posting or sign-off" to={`${base}?tab=workpapers&filter=approved`} />
        <Kpi icon="file" tone="gray" label="Saved drafts" value={papers.length} caption={more ? "Latest 100 shown" : "All versions in this close"} to={`${base}?tab=workpapers`} />
      </div>
      <nav className="toolbar" aria-label="Close sections">
        <div className="segmented">
          {tabs.map(([key, label]) => (
            <Link key={key} to={`${base}?tab=${key}`} aria-current={tab === key ? "page" : undefined}>
              {label}
            </Link>
          ))}
        </div>
      </nav>
      {tab === "overview" && (
        <div className="stack">
          <Coordinator key={`coordinator-${close.id}`} closeId={close.id} />
          <Work key={`work-${close.id}`} closeId={close.id} onPrepared={() => void reload()} />
          <Exceptions key={`exceptions-${close.id}`} closeId={close.id} />
          <EmailReplies closeId={close.id} onReleased={() => void reload()} />
          <PreparationJobs key={`jobs-${close.id}`} closeId={close.id} onComplete={() => void reload()} />
        </div>
      )}
      {tab === "workpapers" && (
        <div className="stack">
          <div className="toolbar">
            <div className="segmented" role="group" aria-label="Filter drafts">
              {Object.entries(filters).map(([key, [label, test]]) => (
                <button key={key} type="button" aria-pressed={filter === key} onClick={() => setParams({ tab: "workpapers", filter: key })}>
                  {label}
                  <span className="count">{papers.filter(test).length}</span>
                </button>
              ))}
            </div>
          </div>
          <div className="rows">
            <div className="rows-head">
              <span>Workpaper</span>
              <span>Review</span>
              <span>Prepared</span>
              <span />
            </div>
            {shown.length === 0 && <p className="empty">No drafts match this view. Prepare work from evidence or change the filter.</p>}
            {shown.map((paper) => (
              <Link className="row" key={paper.id} to={`${base}/workpapers/${paper.id}`}>
                <span className="row-main">
                  <span className="kpi-icon gray" aria-hidden="true">
                    <Icon name="file" size={16} />
                  </span>
                  <span>
                    <strong>{paperLabels[paper.kind]}</strong>
                    <small>
                      Draft #{paper.id}
                      {paper.paper.revises ? ` · revises #${paper.paper.revises}` : ""}
                      {(paper.paper.blockers?.length ?? 0) > 0 ? ` · ${paper.paper.blockers!.length} blocker${paper.paper.blockers!.length === 1 ? "" : "s"}` : ""}
                    </small>
                  </span>
                </span>
                <span>
                  <ReviewPill state={paper.review_state} />
                </span>
                <small>{new Date(paper.created_at).toLocaleString()}</small>
                <Icon name="arrow" size={16} />
              </Link>
            ))}
          </div>
          {more && <p className="note">Showing the 100 most recent drafts. Older records remain saved.</p>}
          <Recurring key={`recurring-${close.id}`} closeId={close.id} onPrepared={() => void reload()} />
          <RecurringAccruals closeId={close.id} onPrepared={() => void reload()} />
          <CarryForward key={close.id} closeId={close.id} />
          <BillCorrection closeId={close.id} />
        </div>
      )}
      {tab === "evidence" && (
        <div className="stack">
          <Evidence key={`evidence-${close.id}`} closeId={close.id} onReplace={() => void reload()} />
          <QuickBooks key={`qbo-${close.id}`} closeId={close.id} entity={close.entity} />
        </div>
      )}
      {tab === "requests" && <Requests closeId={close.id} papers={papers} />}
      {tab === "signoff" && (
        <div className="stack">
          <Readiness key={`readiness-${close.id}`} closeId={close.id} papers={papers} />
          <ProposedStatePreview closeId={close.id} />
          <EffortCard closeId={close.id} />
          <Package close={close} />
        </div>
      )}
      {tab === "settings" && (
        <div className="stack">
          <Procedures key={`procedures-${close.id}`} closeId={close.id} entity={close.entity} onApplied={() => void reload()} />
          <ProductionAccess closeId={close.id} entity={close.entity} />
          <RetentionControls close={close} onDeleted={setDeleted} />
        </div>
      )}
    </>
  );
}
