import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Avatar } from "@close/ui/components/Avatar";
import { Icon } from "@close/ui/components/Icon";
import { PageHeader } from "@close/ui/components/PageHeader";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { Segmented } from "@close/ui/components/Segmented";
import { StatGroup, StatTile } from "@close/ui/components/StatTile";
import type { AttentionInbox } from "../closes/Attention";
import { DelegatedCloses } from "../closes/Goal";
import { NotOpened, type Obligation, type Unopened } from "../closes/Morning";
import { Setup } from "../closes/Setup";
import { workspaceRequest } from "../closes/workspace";
import { BulkReview, type QueueItem } from "../review/BulkReview";
import { DailyBooks } from "./DailyBooks";
import { Dependencies } from "./Dependencies";
import { Goals } from "./Goals";

type HomeCounts = {
  as_of: string;
  closes: number;
  can_add: boolean;
  can_review: boolean;
  signed_off: { done: number; total: number };
  decisions: { count: number; unread: number; team: number };
  deadlines: { overdue: number; due_soon: number; no_due_date: number; not_opened_overdue: number; not_opened_upcoming: number };
  prepared: { total: number; done: number; failed: number; waiting_person: number; running: number; waiting_client: number; cancelled: number; no_draft: number };
  // exported: ended closes whose books are a complete set of labeled historical exports rather than a capture.
  books: { captured: number; of: number; exceptions: number; unchecked: number; exported: number };
  due: Obligation[];
  owed: { close_id: string; entity: string; period: string; due_on: string; owner: "mine" | "other" | "none"; state: "open" | "reopen_required" | "unknown" | "signed_off" }[];
  inbox: AttentionInbox;
  // Months opened early whose sign-off no longer stands: decisions, though outside the owed cohort.
  reopened_later: HomeCounts["owed"];
  // Ended months still open whose books have no usable month-end capture.
  uncaptured: HomeCounts["owed"];
  not_opened: Unopened[];
};

const views = { all: "All", unread: "New", mine: "Assigned to me", snoozed: "Snoozed" } as const;
type View = keyof typeof views;

const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;
// A collapsed list opens before it scrolls into view, so a card reaches what it counted.
const scrollTo = (id: string) => {
  const target = document.getElementById(id);
  if (target instanceof HTMLDetailsElement) target.open = true;
  target?.scrollIntoView({ block: "start" });
};

// Home: the four outcomes across the reader's clients, a line on how far the books are read, and one list of the
// decisions waiting, grouped by client with the most pressing first. Everything else on it shows only when it has rows.
export default function Home() {
  const [counts, setCounts] = useState<HomeCounts>();
  const [error, setError] = useState("");
  const [queue, setQueue] = useState<{ items: QueueItem[]; count: number }>();
  const [queueError, setQueueError] = useState("");
  // The review queue and the dependency lists are capped reads; a client or month filter reaches the rest.
  const [queueClient, setQueueClient] = useState("");
  const [queueLoading, setQueueLoading] = useState(false);
  // Once a batch was approved here, the section stays so its outcomes can be read after the queue empties.
  const [reviewed, setReviewed] = useState(false);
  const [refresh, setRefresh] = useState(0);
  // Every read of the counts (on load, every 30 seconds and after an action) reads the decision list again too. A read
  // that fails keeps its last result on screen beside its own error.
  const [tick, setTick] = useState(0);
  const [view, setView] = useState<View>("all");
  useEffect(() => {
    let live = true;
    const load = () =>
      Promise.all([
        workspaceRequest("/home")
          .then((data: HomeCounts) => live && (setCounts(data), setError("")))
          .catch((e) => live && setError(e instanceof Error ? e.message : "Home is unavailable.")),
        workspaceRequest(`/review-queue?client=${encodeURIComponent(queueClient)}`)
          .then((data) => live && (setQueue(data), setQueueError("")))
          .catch((e) => live && setQueueError(e instanceof Error ? e.message : "The review queue is unavailable."))
          .finally(() => live && setQueueLoading(false)),
      ]).finally(() => live && setTick((t) => t + 1));
    void load();
    const timer = setInterval(load, 30_000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [refresh, queueClient]);
  // A link to a section (#review-queue, #decisions) scrolls there once the section has rendered from its read.
  const scrolledTo = useRef("");
  useEffect(() => {
    const hash = window.location.hash.slice(1);
    if (!hash || scrolledTo.current === hash) return;
    const target = document.getElementById(hash);
    if (target) {
      scrolledTo.current = hash;
      target.scrollIntoView({ block: "start" });
    }
  }, [counts, queue]);
  const bump = () => setRefresh((v) => v + 1);
  const today = (counts?.as_of ?? new Date().toISOString()).slice(0, 10);
  const dueUnopened = counts?.not_opened.filter((m) => m.due_on && m.due_on <= new Date(Date.parse(today) + 7 * 86400000).toISOString().slice(0, 10)) ?? [];
  // The deadline card counts the dated closes overdue or close, the months owed by then and not opened, and the
  // outstanding closes with no date at all; its list shows every one of them.
  const undated = counts?.owed.filter((item) => item.state !== "signed_off" && !item.due_on) ?? [];
  const undatedUnopened = counts?.not_opened.filter((m) => !m.due_on) ?? [];
  const reopened = [...(counts?.owed.filter((item) => item.state === "reopen_required" || item.state === "unknown") ?? []), ...(counts?.reopened_later ?? [])];
  const deadlines = (counts?.due.length ?? 0) + dueUnopened.length + undated.length + undatedUnopened.length > 0;
  // A firm with no close at all and nothing owed has one thing to do: add its first client.
  const empty = counts && counts.closes === 0 && counts.not_opened.length === 0;
  const d = counts?.deadlines;
  const missed = d ? d.overdue + d.not_opened_overdue : undefined;
  // What the heading says is waiting: decisions, overdue closes, months without their books and what the books show
  // new; only with none of these is it all clear.
  const waiting = counts
    ? [
        counts.decisions.count ? n(counts.decisions.count, "decision") + " waiting on you" : "",
        missed ? n(missed, "close") + " overdue" : "",
        counts.uncaptured.length ? n(counts.uncaptured.length, "month") + " without month-end books" : "",
        counts.books.exceptions ? n(counts.books.exceptions, "new exception") + " in the books" : "",
        counts.books.unchecked ? n(counts.books.unchecked, "capture") + " not fully checked" : "",
      ].filter(Boolean)
    : [];
  const soon = d ? d.due_soon + d.not_opened_upcoming : 0;
  const fraction = (done: number, total: number) => (
    <>
      {done}
      <small> of {total}</small>
    </>
  );
  return (
    <section className="bare" aria-label="Home">
      <PageHeader icon="work" title="Home">
        {counts ? (waiting.length ? `${waiting.join(" · ")}.` : "Nothing is waiting on you right now.") : "Your clients' closes at a glance."}
      </PageHeader>
      {error && <p role="alert">{error}</p>}
      <StatGroup>
        <StatTile icon="review" tone="green" label="Signed off" value={counts && fraction(counts.signed_off.done, counts.signed_off.total)} caption="closes owed through this month" onClick={() => scrollTo("owed")} />
        <StatTile
          icon="decision"
          tone="orange"
          label="Need your decision"
          value={counts?.decisions.count}
          caption={counts ? `${counts.decisions.unread} new${counts.decisions.team ? ` · ${counts.decisions.team} for teammates` : ""}` : undefined}
          onClick={() => {
            // The card counts every decision of the reader's, so it opens the whole list.
            setView("all");
            scrollTo("decisions");
          }}
        />
        <StatTile
          icon="accrual"
          tone={missed ? "red" : "gray"}
          label="Deadline exceptions"
          value={counts && (counts.due.length + dueUnopened.length + undated.length + undatedUnopened.length)}
          caption={d ? `${missed} overdue · ${n(soon, "close")} due this week · ${undated.length + undatedUnopened.length} without a date` : undefined}
          onClick={() => scrollTo(deadlines ? "deadlines" : "owed")}
        />
        <StatTile
          icon="agent"
          tone="blue"
          label="Prepared by the agent"
          value={counts && fraction(counts.prepared.done, counts.prepared.total)}
          caption={counts ? `${counts.prepared.failed} failed · ${counts.prepared.waiting_person} waiting on you · ${counts.prepared.waiting_client} on clients${counts.prepared.no_draft ? ` · ${counts.prepared.no_draft} finished with no draft` : ""}${counts.prepared.cancelled ? ` · ${counts.prepared.cancelled} cancelled` : ""}` : undefined}
          to="/dashboard/work?view=all"
        />
      </StatGroup>
      {counts && (
        <p className="trust-strip" aria-label="How current the books are">
          {counts.uncaptured.length > 0 ? (
            <button type="button" className="chip warn" onClick={() => scrollTo("uncaptured")}>
              Books captured for {counts.books.captured} of {counts.books.of}
              {counts.books.exported ? ` · ${counts.books.exported} from exports` : ""}
            </button>
          ) : (
            <span className="chip">
              Books captured for {counts.books.captured} of {counts.books.of}
              {counts.books.exported ? ` · ${counts.books.exported} from exports` : ""}
            </span>
          )}
          {counts.books.exceptions ? (
            <button type="button" className="chip warn" onClick={() => scrollTo("daily-books")}>
              {n(counts.books.exceptions, "new exception")} this and last month
            </button>
          ) : (
            <span className="chip">0 new exceptions this and last month</span>
          )}
          {counts.books.unchecked > 0 && (
            <button type="button" className="chip warn" onClick={() => scrollTo("daily-books")}>
              {n(counts.books.unchecked, "capture")} not fully checked
            </button>
          )}
          <span className="chip">{n(counts.prepared.running, "document")} in preparation</span>
          <span className="chip neutral">Updated {new Date(counts.as_of).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}</span>
        </p>
      )}
      <Goals onChange={bump} />
      {empty ? (
        counts.can_add ? (
          <section className="first-client" aria-label="Add your first client">
            <Icon name="clients" size={24} />
            <span>
              <h2>Add your first client</h2>
              <p>Name the client and open its month. The agent reads the books and the documents you give it and prepares the close; you decide.</p>
            </span>
            <Link className="button primary" to="/dashboard/closes">
              Add a client
            </Link>
          </section>
        ) : (
          <section aria-label="No month open yet">
            <h2>No month is open for your clients yet</h2>
            <p>
              Open one from <Link to="/dashboard/closes">Clients</Link> when a client is assigned to you; a firm administrator assigns clients in the Security center.
            </p>
          </section>
        )
      ) : (
        <>
          <Setup onEnrolled={bump} />
          {deadlines && (
            <>
              <SectionHeader title="Deadlines" id="deadlines">
                <small>Overdue or due within a week and not signed off, and closes with no due date.</small>
              </SectionHeader>
              {counts!.due.length + undated.length > 0 && (
                <RowList label="Due closes">
                  {[...counts!.due, ...undated].map((item) => (
                    <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}`}>
                      <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today) : "No due date"} />
                      {!item.due_on ? <span className="status">No date</span> : item.due_on < today ? <span className="status danger">Overdue</span> : <span className="status warn">Due soon</span>}
                      <small>{{ open: "Not signed off", reopen_required: "Changed since sign-off", unknown: "Sign-off could not be rechecked", signed_off: "Signed off" }[item.state]}</small>
                      <Icon name="arrow" />
                    </Link>
                  ))}
                </RowList>
              )}
              <NotOpened months={[...dueUnopened, ...undatedUnopened]} due />
            </>
          )}
          <Decisions today={today} tick={tick} snapshot={counts?.inbox} reopened={reopened} canReview={counts?.can_review ?? false} teamTotal={counts?.decisions.team ?? 0} view={view} onView={setView} onChange={bump} />
          {counts && counts.uncaptured.length > 0 && (
            <>
              <SectionHeader title="Books to capture" id="uncaptured">
                <small>Ended months with no usable month-end capture: capture them, or label historical exports.</small>
              </SectionHeader>
              <RowList label="Books to capture">
                {counts.uncaptured.map((item) => (
                  <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}?tab=evidence`}>
                    <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today) : "No due date"} />
                    <span className="status warn">No month-end books</span>
                    <span />
                    <Icon name="arrow" />
                  </Link>
                ))}
              </RowList>
            </>
          )}
          <Dependencies refresh={tick} />
          <DailyBooks refresh={tick} canReview={counts?.can_review ?? false} onReviewed={bump} />
          {counts && (counts.owed.length > 0 || counts.not_opened.length > 0) && (
            <details className="owed" id="owed">
              <summary>
                Closes owed through this month · {counts.signed_off.done} of {counts.signed_off.total} signed off
              </summary>
              <RowList label="Closes owed">
                {counts.owed.map((item) => (
                  <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}`}>
                    <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today) : "No due date"} />
                    <span className={item.state === "signed_off" ? "status ok" : item.state === "open" ? "status warn" : "status danger"}>
                      {{ signed_off: "Signed off", open: "Not signed off", reopen_required: "Changed since sign-off", unknown: "Sign-off could not be rechecked" }[item.state]}
                    </span>
                    <span />
                    <Icon name="arrow" />
                  </Link>
                ))}
                {counts.not_opened.map((m) => (
                  <div className="row" key={`${m.entity}:${m.period}`}>
                    <EntityCell name={m.entity} title={`${m.entity} · ${m.period}`} detail={m.due_on ? dueWords(m.due_on, today) : "No due date"} />
                    <span className="status danger">Not opened</span>
                    <span />
                    <span />
                  </div>
                ))}
              </RowList>
            </details>
          )}
        </>
      )}
      {!empty && (queueError || queueClient || reviewed || (queue && queue.count > 0)) && (
        <>
          <SectionHeader title="Ready for your review" id="review-queue">
            {(queueClient || (queue && queue.count > queue.items.length)) && (
              <ClientFilter
                value={queueClient}
                onChange={(next) => {
                  if (next === queueClient) return;
                  // The rows of the previous filter leave before the new read answers.
                  setQueue(undefined);
                  setQueueLoading(true);
                  setQueueClient(next);
                }}
                shown={queue?.items.length ?? 0}
                of={queue?.count ?? 0}
              />
            )}
          </SectionHeader>
          {queueError && <p role="alert">{queueError}{queue ? " Showing the last queue read." : ""}</p>}
          {queueLoading ? (
            <p role="status" className="empty">Reading the queue…</p>
          ) : (
            <BulkReview
              items={queue?.items}
              onDone={() => {
                setReviewed(true);
                bump();
              }}
            />
          )}
        </>
      )}
      {!empty && <DelegatedCloses refresh={tick} client="" />}
      {!empty && counts && <NotOpened months={counts.not_opened} />}
    </section>
  );
}

const dueWords = (dueOn: string, today: string) => {
  if (!dueOn) return "";
  const date = new Date(`${dueOn}T00:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" });
  return dueOn < today ? `Overdue since ${date}` : `Due ${date}`;
};

// A client or month filter for a capped list, with what the list shows of its whole.
export function ClientFilter({ value, onChange, shown, of }: { value: string; onChange: (value: string) => void; shown: number; of: number }) {
  return (
    <form
      className="client-filter"
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        onChange(String(new FormData(e.currentTarget).get("client") ?? "").trim());
      }}
    >
      <small>{value ? `Matching “${value}”` : of ? `Showing ${shown} of ${of}` : `Showing the first ${shown}`}</small>
      <input name="client" aria-label="Filter by client or close month" placeholder="Client or YYYY-MM" maxLength={200} defaultValue={value} key={value} />
      <button>Filter</button>
    </form>
  );
}

// The server orders the events (overdue closes first, then by due date, undated last) before paging, so page one
// holds the most pressing close; the first group here is it.
type Reopened = HomeCounts["owed"][number];

// The default list (every decision, page one) comes with the counts, read in their snapshot; another view or page is
// read on its own, and any action re-reads the counts, which bring the list again.
function Decisions({ today, tick, snapshot, reopened, canReview, teamTotal, view, onView, onChange }: { today: string; tick: number; snapshot?: AttentionInbox; reopened: Reopened[]; canReview: boolean; teamTotal: number; view: View; onView: (view: View) => void; onChange: () => void }) {
  const [page, setPage] = useState(0);
  const [read, setRead] = useState<AttentionInbox>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  const fromSnapshot = view === "all" && page === 0;
  const inbox = fromSnapshot ? snapshot : read;
  useEffect(() => {
    if (fromSnapshot) return;
    let live = true;
    workspaceRequest(`/attention?page=${page}&view=${view}&order=priority`)
      .then((d: AttentionInbox) => {
        if (!live) return;
        setRead(d);
        setPage(d.page);
        setError("");
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Decisions are unavailable."));
    return () => {
      live = false;
    };
  }, [fromSnapshot, view, page, version, tick]);
  async function act(path: string, body: unknown) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path, body);
      setVersion((v) => v + 1);
      onChange();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The change could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  const mark = (action: string, keys: string[], until?: string) => void act("/attention", { action, keys, until });
  const later = (days: number) => new Date(Date.now() + days * 86400000).toISOString();
  // The list the card opens holds the reader's own decisions; what waits on a teammate's authority sits apart below,
  // so the headline and the list name the same items.
  const groups = (inbox?.groups ?? []).map((g) => ({ ...g, events: g.events.filter((e) => e.yours) })).filter((g) => g.events.length > 0);
  const team = (inbox?.groups ?? []).map((g) => ({ ...g, events: g.events.filter((e) => !e.yours) })).filter((g) => g.events.length > 0);
  // A reopened close has no read or snooze mark: it shows in All and, when it is the reader's, in Assigned to me.
  const reopenedShown = view === "all" || view === "mine" ? reopened.filter((item) => view !== "mine" || item.owner === "mine") : [];
  const teamReopened = canReview ? [] : reopenedShown;
  return (
    <>
      <SectionHeader title="Decisions" id="decisions">
        <Segmented
          label="Show"
          items={(Object.keys(views) as View[]).map((key) => ({ key, label: views[key] }))}
          value={view}
          onChange={(key) => {
            onView(key as View);
            setPage(0);
          }}
        />
      </SectionHeader>
      {error && <p role="alert">{error}</p>}
      {canReview && reopenedShown.length > 0 && (
        <RowList label="Signed off, then changed">
          {reopenedShown.map((item) => (
            <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}?tab=signoff`}>
              <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today) : "No due date"} />
              <span className="status danger">{item.state === "unknown" ? "Sign-off could not be rechecked" : "Changed since sign-off"}</span>
              <small>Reopen and review, or sign off again</small>
              <Icon name="arrow" />
            </Link>
          ))}
        </RowList>
      )}
      {inbox && groups.length === 0 && (!canReview || reopenedShown.length === 0) && <p className="empty">{view === "snoozed" ? "Nothing is snoozed." : view === "unread" ? "Nothing new." : "Nothing needs you here."}</p>}
      {groups.map((g, index) => {
        const keys = g.events.map((e) => e.key);
        return (
          <article className={index === 0 && view !== "snoozed" ? "decision spotlight" : "decision"} key={g.close_id}>
            <header>
              <Avatar label={g.entity} />
              <span className="decision-title">
                <h3>
                  <Link to={`/dashboard/closes/${g.close_id}`}>
                    {g.entity} · {g.period}
                  </Link>
                </h3>
                <small>
                  {[dueWords(g.due_on, today), g.priority !== "normal" ? `${g.priority} priority` : "", g.owner === "mine" ? "Yours" : g.owner === "other" ? "With a teammate" : "Unassigned"].filter(Boolean).join(" · ")}
                </small>
              </span>
              {index === 0 && view !== "snoozed" && <span className="pill">Do first</span>}
              {g.owner !== "other" && (
                <button disabled={busy} onClick={() => void act(`/${g.close_id}/planning`, { due_on: g.due_on, priority: g.priority, assignment: g.owner === "mine" ? "release" : "take" })}>
                  {g.owner === "mine" ? "Release" : "Take"}
                </button>
              )}
            </header>
            <ul>
              {g.events.map((e) => (
                <li key={e.key}>
                  <Link to={e.workpaper_id ? `/dashboard/closes/${g.close_id}/workpapers/${e.workpaper_id}` : `/dashboard/closes/${g.close_id}${e.job_id ? `?job=${e.job_id}` : e.account_id || e.kind === "unanswered" ? "#coverage-work" : ""}`}>{e.text}</Link>
                  <small>
                    {!e.read && <span className="pill info">New</span>} {new Date(e.at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}
                    {e.snoozed_until && ` · snoozed until ${new Date(e.snoozed_until).toLocaleDateString(undefined, { day: "numeric", month: "short" })}`}
                  </small>
                  <button disabled={busy} onClick={() => mark(e.read ? "unread" : "read", [e.key])}>
                    {e.read ? "Mark new" : "Mark seen"}
                  </button>
                </li>
              ))}
            </ul>
            <footer>
              {view === "snoozed" ? (
                <button disabled={busy} onClick={() => mark("wake", keys)}>
                  Wake
                </button>
              ) : (
                <>
                  <button disabled={busy} onClick={() => mark("snooze", keys, later(1))}>
                    Snooze a day
                  </button>
                  <button disabled={busy} onClick={() => mark("snooze", keys, later(7))}>
                    Snooze a week
                  </button>
                </>
              )}
            </footer>
          </article>
        );
      })}
      {(team.length > 0 || teamReopened.length > 0 || (view === "all" && teamTotal > 0)) && (
        <details className="team">
          <summary>For teammates · {view === "all" ? teamTotal : team.reduce((n, g) => n + g.events.length, 0) + teamReopened.length}</summary>
          <p className="note">
            Waiting on a reviewer's or a sender's decision, not yours.
            {view === "all" && teamTotal > team.reduce((n, g) => n + g.events.length, 0) && ` Showing ${team.reduce((n, g) => n + g.events.length, 0)} here; the rest follow your own decisions on the later pages.`}
          </p>
          {teamReopened.map((item) => (
              <article className="decision" key={`team-reopened-${item.close_id}`}>
                <header>
                  <Avatar label={item.entity} />
                  <span className="decision-title">
                    <h3>
                      <Link to={`/dashboard/closes/${item.close_id}?tab=signoff`}>
                        {item.entity} · {item.period}
                      </Link>
                    </h3>
                    <small>{item.state === "unknown" ? "Sign-off could not be rechecked" : "Changed since sign-off"}</small>
                  </span>
                </header>
              </article>
            ))}
          {team.map((g) => (
            <article className="decision" key={`team-${g.close_id}`}>
              <header>
                <Avatar label={g.entity} />
                <span className="decision-title">
                  <h3>
                    <Link to={`/dashboard/closes/${g.close_id}`}>
                      {g.entity} · {g.period}
                    </Link>
                  </h3>
                </span>
              </header>
              <ul>
                {g.events.map((e) => (
                  <li key={e.key}>
                    <Link to={e.workpaper_id ? `/dashboard/closes/${g.close_id}/workpapers/${e.workpaper_id}` : `/dashboard/closes/${g.close_id}${e.job_id ? `?job=${e.job_id}` : ""}`}>{e.text}</Link>
                    <small>
                      {new Date(e.at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}
                      {e.snoozed_until && ` · snoozed until ${new Date(e.snoozed_until).toLocaleDateString(undefined, { day: "numeric", month: "short" })}`}
                    </small>
                    {/* A snooze set before the item became a teammate's can still be lifted here. */}
                    {view === "snoozed" && (
                      <button disabled={busy} onClick={() => mark("wake", [e.key])}>
                        Wake
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </article>
          ))}
        </details>
      )}
      {(page > 0 || inbox?.has_more) && (
        <p className="toolbar">
          <button disabled={page === 0} onClick={() => setPage(page - 1)}>
            Newer
          </button>
          <button disabled={!inbox?.has_more} onClick={() => setPage(page + 1)}>
            Older
          </button>
        </p>
      )}
      <p className="note">
        Which decisions count as new, and browser notifications, are set under <Link to="/dashboard/settings?tab=notifications">Settings → Notifications</Link>.
      </p>
    </>
  );
}
