import { Link, useSearchParams } from "react-router";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { useDates } from "../../data/time";
import { CarryForward } from "./CarryForward";
import type { Status } from "./Goal";
import { AgentPanel, entriesOf, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import { Recurring } from "./Recurring";
import { RecurringAccruals } from "./RecurringAccruals";
import type { Brief } from "./Stands";
import { amount } from "./workspace";
import { needsAttention, paperLabel, ReviewPill, readyForReview, type Close, type Paper } from "./WorkpaperView";

// A close's Workpapers (docs/design-system.md, Close page): how many drafts wait for review in one sentence, then the
// decisions on this tab (drafts ready for approval open in place, one after another) and the drafts as one table with
// the documents the agent is still preparing; the rail sums what waits for approval and opens the tab's other views
// (recurring schedules and accruals, earlier versions, carrying work into the next month).

const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"];
const capital = (text: string) => text[0].toUpperCase() + text.slice(1);
const counted = (n: number, one: string) => `${words[n] ?? n} ${one}${n === 1 ? "" : "s"}`;
// The drafts in force: a draft that a later one revises is an earlier version.
const currentOf = (papers: Paper[]) => papers.filter((p) => !papers.some((q) => q.paper.revises === p.id));

// WorkpapersLede says how many drafts wait for review, or need another look, or that every draft is approved; under it
// how many drafts are current and approved, and that approval is not posting.
export function WorkpapersLede({ papers, more }: { papers: Paper[]; more: boolean }) {
  const current = currentOf(papers);
  const ready = current.filter(readyForReview).length;
  const again = current.filter(needsAttention).length;
  const approved = current.filter((p) => p.review_state === "approved").length;
  const sentence = ready
    ? `${capital(counted(ready, "draft"))} ${ready === 1 ? "is" : "are"} ready for your review.`
    : again
      ? `${capital(counted(again, "draft"))} need${again === 1 ? "s" : ""} another look.`
      : current.length && approved === current.length
        ? more
          ? "The current drafts among the latest 100 are all approved."
          : "Every draft is approved."
        : current.length
          ? "No draft is ready for review yet."
          : "No drafts yet.";
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>
        {[current.length ? `${current.length} current draft${current.length === 1 ? "" : "s"}${more ? " among the latest 100" : ""}` : "", current.length ? `${approved} approved` : "", "approval is not posting or sign-off"].filter(Boolean).join(" · ")}
      </p>
    </div>
  );
}

// A draft's status in the close's words, and its tone; a document still with the agent or the client has its own.
function draftStatus(p: Paper): [string, string] {
  if (p.review_state === "approved") return ["Approved", ""];
  if (p.review_state === "changes_requested") return ["Changes requested", "danger"];
  if (p.review_state === "source_changed") return ["Source changed", "danger"];
  if ((p.paper.blockers?.length ?? 0) > 0) return ["Blocked", "danger"];
  return readyForReview(p) ? ["Ready for review", "muted"] : ["Needs its sources", "neutral"];
}
// The filter's groups; the old ?filter= links (ready, attention, approved) choose theirs.
const groups: [string, string, (status: string) => boolean][] = [
  ["ready", "Ready for review", (s) => s === "Ready for review"],
  ["preparing", "Preparing", (s) => s === "Preparing"],
  ["client", "Waiting for client", (s) => s === "Waiting for client"],
  ["decide", "Needs your decision", (s) => s === "Needs your decision"],
  ["attention", "Needs another look", (s) => s === "Changes requested" || s === "Source changed" || s === "Blocked"],
  ["approved", "Approved", (s) => s === "Approved"],
];
type Row = { key: string; label: string; id?: string; status: [string, string]; amount?: string; at?: string; to: string };

// What a draft proposes this month, in cents and its currency: every entry it books now, not next month's reversal.
function monthCents(p: Paper) {
  const entries = entriesOf(p).filter((e) => e.role !== "reversal");
  if (!entries.length) return undefined;
  const cents = entries.reduce((n, { entry }) => {
    const [whole, fraction = ""] = entry.amount.replace("-", "").split(".");
    return n + Number(whole) * 100 + Number((fraction + "00").slice(0, 2));
  }, 0);
  return { cents, currency: (p.paper as { input?: { currency?: string } }).input?.currency ?? "USD" };
}
const money = (cents: number, currency: string) => amount(`${Math.floor(cents / 100)}.${String(cents % 100).padStart(2, "0")}`, currency);

// Drafts is the tab's one table: the current drafts, newest first, then the documents the agent is still preparing or
// waiting on the client for, filtered by status.
function Drafts({ papers, status, statusError, base }: { papers: Paper[]; status: Status | undefined; statusError: "" | "stale" | "failed"; base: string }) {
  const dates = useDates();
  const [params, setParams] = useSearchParams();
  const rows: Row[] = [
    ...currentOf(papers).map((p) => {
      const month = monthCents(p);
      return { key: p.id, label: paperLabel(p), id: p.id, status: draftStatus(p), amount: month ? money(month.cents, month.currency) : undefined, at: p.created_at, to: `${base}/workpapers/${p.id}` };
    }),
    // A document with no draft yet stands for its draft-to-be: the agent prepares it, the client owes it, or a person
    // confirms what was read.
    ...(status?.progress.items ?? [])
      .filter((item) => !item.drafts.length && (item.status === "working" || item.status === "client" || item.status === "needs_you"))
      .map((item) => ({
        key: `job-${item.job_id}`,
        label: item.name,
        status: (item.status === "working" ? ["Preparing", "live"] : item.status === "client" ? ["Waiting for client", "muted"] : ["Needs your decision", "muted"]) as [string, string],
        to: `${base}?job=${item.job_id}`,
      })),
  ];
  const counts = new Map(groups.map(([key, , test]) => [key, rows.filter((row) => test(row.status[0])).length]));
  const asked = params.get("filter") ?? "all";
  const filter = groups.some(([key]) => key === asked) ? asked : "all";
  const shown = filter === "all" ? rows : rows.filter((row) => groups.find(([key]) => key === filter)![2](row.status[0]));
  return (
    <section className="bare ks-set" aria-labelledby="drafts-title">
      <div className="ks-sec">
        <h2 id="drafts-title">Drafts</h2>
        <small>latest first</small>
        {(rows.length > 0 || filter !== "all") && (
          <div className="ks-end">
            <Segmented
              label="Filter drafts"
              items={[{ key: "all", label: "All", count: rows.length }, ...groups.filter(([key]) => counts.get(key) || key === filter).map(([key, label]) => ({ key, label, count: counts.get(key) }))]}
              value={filter}
              onChange={(key) => setParams({ tab: "workpapers", filter: key })}
            />
          </div>
        )}
      </div>
      {statusError && <p role="alert">{statusError === "failed" ? "The documents still in preparation could not be read; this list may be incomplete." : "The documents still in preparation could not be refreshed; showing the last read."}</p>}
      <div className="ks-table ks-drafts" role="table" aria-labelledby="drafts-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Draft</span>
          <span role="columnheader">Status</span>
          <span role="columnheader" className="r" title="The total of the entries the draft books this month">
            Amount
          </span>
          <span role="columnheader">Prepared</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {shown.map((row) => (
          <div className="ks-tr linked" role="row" key={row.key}>
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={row.to}>
                {row.label}
                {row.id && <small> · #{row.id}</small>}
              </Link>
            </span>
            <span role="cell">
              <span className={`pill ${row.status[1]}`}>{row.status[0]}</span>
            </span>
            <span className="r" role="cell">
              {row.amount ?? "—"}
            </span>
            <span className="ks-mut" role="cell">
              {row.at ? dates.dayMonth(row.at) : "—"}
            </span>
            <span role="cell">
              <Icon name="arrow" className="ks-go" />
            </span>
          </div>
        ))}
        {rows.length === 0 && <p className="ks-empty-row">No drafts yet. The agent prepares them from the documents, or prepare one by hand from the ⋯ menu.</p>}
        {rows.length > 0 && shown.length === 0 && <p className="ks-empty-row">None now.</p>}
      </div>
    </section>
  );
}

// Versions lists every saved draft of the close, earlier versions included, as the tab listed them before.
function Versions({ papers, more, base }: { papers: Paper[]; more: boolean; base: string }) {
  const dates = useDates();
  return (
    <section className="bare ks-set" aria-label="Every saved draft">
      <div className="ks-table ks-versions" role="table" aria-label="Every saved draft">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Draft</span>
          <span role="columnheader">Review</span>
          <span role="columnheader">Prepared</span>
        </div>
        {papers.map((p) => (
          <div className="ks-tr linked" role="row" key={p.id}>
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={`${base}/workpapers/${p.id}`}>
                {paperLabel(p)}
                <small>
                  {" "}
                  · #{p.id}
                  {p.paper.revises ? ` · revises #${p.paper.revises}` : ""}
                </small>
              </Link>
            </span>
            <span role="cell">
              <ReviewPill state={p.review_state} />
            </span>
            <span className="ks-mut" role="cell">
              {dates.dateTime(p.created_at)}
            </span>
          </div>
        ))}
        {papers.length === 0 && <p className="ks-empty-row">No drafts saved yet.</p>}
      </div>
      {more && <p className="ks-table-foot">Showing the 100 most recent drafts. Older records remain saved.</p>}
    </section>
  );
}

const views: Record<string, string> = {
  recurring: "Recurring schedules",
  accruals: "Recurring accruals",
  versions: "Every saved draft",
  carry: "Carry work into the next month",
};

// Workpapers is the tab's body: on the left what needs the person here and the drafts, or one of the tab's views; on
// the right what waits for approval and what is approved, the agent and the tab's other views.
export function Workpapers({ close, papers, more, needs, brief, briefRead, decisionsError, status, statusError, base, onPrepared }: { close: Close; papers: Paper[]; more: boolean; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; statusError: "" | "stale" | "failed"; base: string; onPrepared: () => void }) {
  const [params] = useSearchParams();
  const view = views[params.get("view") ?? ""] ? params.get("view")! : "";
  const current = currentOf(papers);
  // Amounts are summed only within one currency: a total across currencies would be no amount at all.
  const total = (list: Paper[]) => {
    const months = list.map(monthCents).filter((m) => m !== undefined);
    if (!months.length || new Set(months.map((m) => m.currency)).size > 1) return undefined;
    return money(months.reduce((n, m) => n + m.cents, 0), months[0].currency).replace(/\.00$/, "");
  };
  const waiting = total(current.filter(readyForReview));
  const done = total(current.filter((p) => p.review_state === "approved"));
  const carried = current.filter((p) => p.kind === "prepaid" && p.paper.carry_forward).length;
  const earlier = papers.length - current.length;
  const figures: Figure[] = [
    ...(waiting ? ([[waiting, "waiting for your approval", "att"]] as Figure[]) : []),
    ...(done ? ([[done, "approved"]] as Figure[]) : []),
    ...(carried ? ([[String(carried), carried === 1 ? "schedule carried from last month" : "schedules carried from last month"]] as Figure[]) : []),
    ...(earlier ? ([[String(earlier), earlier === 1 ? "earlier version" : "earlier versions"]] as Figure[]) : []),
  ];
  const to = (name: string) => `${base}?tab=workpapers&view=${name}`;
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "workpapers") } : undefined;
  const currency = close.profile?.currency ?? "USD";
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=workpapers`}>Drafts</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "recurring" && <Recurring key={`recurring-${close.id}`} closeId={close.id} currency={currency} onPrepared={onPrepared} open />}
              {view === "accruals" && <RecurringAccruals closeId={close.id} currency={currency} onPrepared={onPrepared} open />}
              {view === "versions" && <Versions papers={papers} more={more} base={base} />}
              {view === "carry" && <CarryForward key={close.id} closeId={close.id} open />}
            </div>
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            <Drafts papers={papers} status={status} statusError={statusError} base={base} />
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the workpapers">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Workpapers"
          links={[
            { label: views.recurring, to: to("recurring") },
            { label: views.accruals, to: to("accruals") },
            { label: views.versions, note: earlier ? `${earlier} earlier` : undefined, to: to("versions") },
            { label: views.carry, to: to("carry") },
          ]}
        />
      </aside>
    </div>
  );
}
