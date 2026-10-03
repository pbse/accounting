import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import type { IconName } from "@close/ui/components/Icon";
import { amount, workspaceRequest } from "../closes/workspace";
import { CashPlan } from "../closes/CashPlan";
import { Budget } from "../closes/Budget";
import { PackView, Publication, type Pack } from "../closes/Reports";

type Close = { id: string; entity: string; period: string };
type Published = {
  close_id: string;
  entity: string;
  period: string;
  publication_id: string;
  version: number;
  published_at: string;
  acknowledged: boolean;
  currency: string;
  net_income: string;
  book_cash: string;
  working_capital: string;
};
type Overview = { executive: boolean; closes?: Close[]; published?: Published[] };
export type ExecutiveView = "overview" | "reports" | "cash" | "decisions";

const pages: Record<ExecutiveView, [string, IconName, string]> = {
  overview: ["Business overview", "measure", "Each client's newest published management pack. Published figures are reviewed and signed off; they do not change."],
  reports: ["Reports", "file", "Published versions of the month's management pack, with PDF and spreadsheet copies, and the current comparison with the approved budget."],
  cash: ["Cash plan", "reconcile", "The next 13 weeks of book cash, prepared from the books and approved schedules. It is a working forecast, not reviewed results."],
  decisions: ["My decisions", "decision", "Published reports waiting for your acknowledgment. Acknowledging records that you received a version; it approves nothing."],
};

// A client's executive: published results, the cash plan and their own acknowledgments, for the clients they are given.
export default function Executive({ view }: { view: ExecutiveView }) {
  const [params, setParams] = useSearchParams();
  const [data, setData] = useState<Overview>();
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/executive")
      .then((value) => live && (setData(value), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Your reports are unavailable."));
    return () => {
      live = false;
    };
  }, [version]);
  const [title, icon, description] = pages[view];
  const header = (
    <PageHeader icon={icon} title={title}>
      {description}
    </PageHeader>
  );
  if (!data) return error ? <>{header}<p role="alert">{error}</p></> : <>{header}<p role="status">Opening your reports…</p></>;
  if (!data.executive)
    return (
      <>
        {header}
        <p className="empty">This view is for a client's executives. Accountants open the same reports, cash plan and budget from each client close.</p>
      </>
    );
  const closes = data.closes ?? [];
  const published = data.published ?? [];
  const close = closes.find((c) => c.id === params.get("close")) ?? closes[0];
  const show = (p: Published, value: string) => (value ? amount(value, p.currency) : "–");
  const picker = close && (
    <label>
      Client and month{" "}
      <select value={close.id} onChange={(e) => setParams({ close: e.target.value })}>
        {closes.map((c) => (
          <option key={c.id} value={c.id}>
            {c.entity} · {c.period}
          </option>
        ))}
      </select>
    </label>
  );
  async function acknowledge(p: Published) {
    try {
      await workspaceRequest(`/${p.close_id}/management-pack/publications`, { action: "acknowledge", publication_id: p.publication_id });
      setVersion((n) => n + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The acknowledgment could not be recorded.");
    }
  }
  return (
    <>
      {header}
      {error && <p role="alert">{error}</p>}
      {closes.length === 0 && <p className="empty">No client is assigned to you yet. Ask your accountant to give you access.</p>}
      {view === "overview" &&
        (published.length === 0 ? (
          closes.length > 0 && <p className="empty">Nothing is published yet. Your accountant publishes each month's pack once it is reviewed and signed off.</p>
        ) : (
          <ul>
            {published.map((p) => (
              <li key={p.close_id}>
                <Link to={`/dashboard/executive/reports?close=${p.close_id}`}>
                  <strong>
                    {p.entity} · {p.period}
                  </strong>
                </Link>{" "}
                · version {p.version}, published {new Date(p.published_at).toLocaleDateString()} · net income {show(p, p.net_income)} · book cash {show(p, p.book_cash)} · working
                capital {show(p, p.working_capital)} ·{" "}
                {p.acknowledged ? <span className="status ok">Acknowledged</span> : <span className="status warn">Waiting for your acknowledgment</span>}
              </li>
            ))}
          </ul>
        ))}
      {view === "reports" && close && (
        <>
          {picker}
          <PublishedReport key={close.id} closeId={close.id} newest={published.find((p) => p.close_id === close.id)?.publication_id} />
          <Budget key={`budget-${close.id}`} closeId={close.id} readOnly />
        </>
      )}
      {view === "cash" && close && (
        <>
          {picker}
          <CashPlan key={`cash-${close.id}`} closeId={close.id} readOnly />
        </>
      )}
      {view === "decisions" &&
        closes.length > 0 &&
        (published.filter((p) => !p.acknowledged).length === 0 ? (
          <p className="empty">Nothing is waiting for you.</p>
        ) : (
          <ul>
            {published
              .filter((p) => !p.acknowledged)
              .map((p) => (
                <li key={p.close_id}>
                  {p.entity} · {p.period}: version {p.version} of the management pack.{" "}
                  <Link to={`/dashboard/executive/reports?close=${p.close_id}`}>Read it</Link>{" "}
                  <button type="button" onClick={() => void acknowledge(p)}>
                    Acknowledge
                  </button>
                </li>
              ))}
          </ul>
        ))}
    </>
  );
}

// The close's published versions; the newest is shown as it was frozen, and any other can be opened.
function PublishedReport({ closeId, newest }: { closeId: string; newest?: string }) {
  const [open, setOpen] = useState(newest);
  const [shown, setShown] = useState<{ publication: { version: number; commentary: string }; pack: Extract<Pack, { state: "ready" }> }>();
  const [error, setError] = useState("");
  useEffect(() => {
    if (!open) return;
    let live = true;
    workspaceRequest(`/${closeId}/management-pack/publications/${open}`)
      .then((value) => live && (setShown(value), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "This version is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, open]);
  return (
    <>
      <Publication closeId={closeId} onOpen={setOpen} />
      {error && <p role="alert">{error}</p>}
      {shown && (
        <>
          <SectionHeader title={`Version ${shown.publication.version}, as published`} />
          <p>{shown.publication.commentary}</p>
          <PackView closeId={closeId} pack={shown.pack} />
        </>
      )}
    </>
  );
}
