import { useDates } from "../../data/time";
import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { amount, workspaceRequest } from "../closes/workspace";
import { bufferWords, CashPlan, type Item, type Obligation, type Plan, type Scenario } from "../closes/CashPlan";
import { cashPlanQuery } from "../../data/cashPlan";
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
  state?: "current" | "needs_correction" | "signoff_lapsed" | "superseded" | "unverified";
};
// A published version is frozen; when the books change after it, the firm publishes a correction and the version says so.
const standing: Record<string, [string, string]> = {
  needs_correction: ["status warn", "The books changed since; a correction is due"],
  signoff_lapsed: ["status warn", "Its sign-off no longer stands; a correction may follow"],
  unverified: ["status info", "Could not be checked against the books right now"],
};
type Overview = { executive: boolean; closes?: Close[]; published?: Published[] };
type ExecutiveView = "overview" | "reports" | "cash" | "decisions";

const pages: Record<ExecutiveView, [string, string]> = {
  overview: ["Business overview", "What changed in the newest reviewed results, where cash is heading and what is overdue, then each client's newest published management pack. A published version is reviewed, signed off and never edited; if the books change afterwards, it is marked and the firm publishes a correction."],
  reports: ["Reports", "Published versions of the month's management pack, with PDF and spreadsheet copies, and the current comparison with the approved budget."],
  cash: ["Cash plan", "The next 13 weeks of book cash, prepared from the books and approved schedules. It is a working forecast, not reviewed results."],
  decisions: ["My decisions", "Published reports waiting for your acknowledgment. Acknowledging records that you received a version; it approves nothing."],
};

// A client's executive: published results, the cash plan and their own acknowledgments, for the clients they are given.
export default function Executive({ view }: { view: ExecutiveView }) {
  const [params, setParams] = useSearchParams();
  // The layout's loader reads this with every workspace page.
  const read = useQuery(workspaceQuery<Overview>("/executive"));
  const data = read.data;
  const dates = useDates();
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "Your reports are unavailable." : "");
  const [title, description] = pages[view];
  const header = (
    <PageHeader title={title}>
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
      setError("");
      void read.refetch();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The acknowledgment could not be recorded.");
    }
  }
  return (
    <>
      {header}
      {error && <p role="alert">{error}</p>}
      {closes.length === 0 && <p className="empty">No client is assigned to you yet. Ask your accountant to give you access.</p>}
      {view === "overview" && close && (
        <>
          {closes.length > 1 && picker}
          <Briefing key={close.id} close={close} published={published.find((p) => p.close_id === close.id)} newest={published.find((p) => p.entity.toLowerCase() === close.entity.toLowerCase())} />
        </>
      )}
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
                · version {p.version}, published {dates.date(p.published_at)} · net income {show(p, p.net_income)} · book cash {show(p, p.book_cash)} · working
                capital {show(p, p.working_capital)} ·{" "}
                {p.state && standing[p.state] && (
                  <>
                    <span className={standing[p.state][0]}>{standing[p.state][1]}</span> ·{" "}
                  </>
                )}
                {p.acknowledged ? <span className="status ok">Acknowledged</span> : <span className="status warn">Waiting for your acknowledgment</span>}
              </li>
            ))}
          </ul>
        ))}
      {view === "reports" && close && (
        <>
          {picker}
          <PublishedReport key={`${close.id}/${params.get("publication") ?? ""}`} closeId={close.id} newest={params.get("publication") ?? published.find((p) => p.close_id === close.id)?.publication_id} />
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
                  {p.state && standing[p.state] && <span className={standing[p.state][0]}>{standing[p.state][1]}</span>}{" "}
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

type Explanation = { account_id: string; name: string; change: string; material: boolean; text: string };

// The briefing for one client and month, from what the executive can already read: what materially changed in the newest
// published pack (its reviewed explanations and the firm's commentary), the lowest book cash the 13-week plan forecasts
// and when, and the overdue invoices and bills it has no new expected date for (one promised or scheduled later falls in
// its weeks instead). Each says what it is: reviewed results, or a forecast with its
// readiness and the date its book cash was read.
function Briefing({ close, published, newest }: { close: Close; published?: Published; newest?: Published }) {
  const dates = useDates();
  const version = useQuery({ ...workspaceQuery<{ publication: { commentary: string; explanations?: Explanation[]; state?: Published["state"] } }>(`/${close.id}/management-pack/publications/${published?.publication_id}`), enabled: !!published });
  const cash = useQuery(cashPlanQuery<Plan, Obligation, Scenario>(close.id, "", true));
  const plan = cash.data?.plan;
  const ready = plan?.state === "ready" ? plan : undefined;
  const material = (version.data?.publication.explanations ?? []).filter((e) => e.material).slice(0, 3);
  const overdue = (prefix: string) => (ready?.past_due ?? []).filter((i) => i.key?.startsWith(prefix));
  const items = (list: Item[]) => list.slice(0, 3).map((i) => `${i.label} ${amount(i.amount.replace(/^-/, ""), ready!.currency)}, due ${i.on}`).join("; ");
  const collections = overdue("invoice:");
  const bills = overdue("bill:");
  // The version's own standing, as its read gives it now: the overview's list carries it for recent months only.
  const state = version.data?.publication.state ?? published?.state;
  return (
    <section aria-label={`Briefing for ${close.entity}, ${close.period}`}>
      <SectionHeader title={`${close.entity} · ${close.period}`} />
      {published ? (
        <p>
          Net income {published.net_income ? amount(published.net_income, published.currency) : "–"} in the reviewed results, version {published.version} published {dates.date(published.published_at)}
          {state && standing[state] ? <> · <span className={standing[state][0]}>{standing[state][1]}</span></> : ""}.
        </p>
      ) : (
        <p>
          No results are published for this month yet; your accountant publishes them once reviewed and signed off.
          {newest && (
            <>
              {" "}
              The newest published results are <Link to={`?close=${newest.close_id}`}>{newest.entity} · {newest.period}</Link>.
            </>
          )}
        </p>
      )}
      {version.error && <p role="alert">The published version could not be read, so what changed is not shown.</p>}
      {material.length > 0 && (
        <ul aria-label="What materially changed">
          {material.map((e) => (
            <li key={e.account_id}>
              <strong>{e.name}</strong> {e.change.startsWith("-") ? "down" : "up"} {amount(e.change.replace(/^-/, ""), published!.currency)}: {e.text}
            </li>
          ))}
        </ul>
      )}
      {version.data?.publication.commentary && <p>From your accountant: {version.data.publication.commentary}</p>}
      {cash.error ? (
        <p role="alert">The cash plan could not be read.</p>
      ) : plan?.state === "waiting" ? (
        <p>Cash plan: {plan.reason}</p>
      ) : ready ? (
        <>
          <p role={ready.lowest.amount.startsWith("-") ? "alert" : undefined}>
            Lowest forecast book cash: <strong>{amount(ready.lowest.amount, ready.currency)}</strong> on {ready.lowest.on}
            {ready.lowest.amount.startsWith("-") ? ", below zero" : ""}, over the next {ready.horizon.weeks} weeks. A{" "}
            {ready.readiness.state === "complete" ? "complete" : ready.readiness.state} forecast{ready.review?.state === "current" ? ", reviewed by your accountant" : ", not yet reviewed"}, from book cash as of {ready.opening.as_of}
            {ready.opening.stale ? " (not current)" : ""}.
          </p>
          <p role={ready.headroom?.startsWith("-") ? "alert" : undefined}>{bufferWords(ready.buffer, ready.headroom, ready.currency)}</p>
          {collections.length > 0 && (
            <p>
              {collections.length} overdue {collections.length === 1 ? "invoice" : "invoices"} to collect with no new expected date: {items(collections)}
              {collections.length > 3 ? ", and more" : ""}.
            </p>
          )}
          {bills.length > 0 && (
            <p>
              {bills.length} overdue {bills.length === 1 ? "bill" : "bills"} to pay with no new expected date: {items(bills)}
              {bills.length > 3 ? ", and more" : ""}.
            </p>
          )}
        </>
      ) : (
        <p role="status">Reading the cash plan…</p>
      )}
      <p>
        <Link to={`/dashboard/executive/reports?close=${close.id}`}>Open the report</Link> · <Link to={`/dashboard/executive/cash?close=${close.id}`}>Open the cash plan</Link>
      </p>
    </section>
  );
}

// The close's published versions; the newest is shown as it was frozen, or the version a link names (an agent's
// citation opens the version it cited), and any other can be opened.
function PublishedReport({ closeId, newest }: { closeId: string; newest?: string }) {
  const [open, setOpen] = useState(newest);
  const [shown, setShown] = useState<{ publication: { version: number; commentary: string; explanations?: { account_id: string; text: string }[] }; pack: Extract<Pack, { state: "ready" }> }>();
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
          {(shown.publication.explanations?.length ?? 0) > 0 && <ul aria-label="Line explanations">{shown.publication.explanations!.map((e) => <li key={e.account_id}>{e.text}</li>)}</ul>}
          <PackView closeId={closeId} pack={shown.pack} />
        </>
      )}
    </>
  );
}
