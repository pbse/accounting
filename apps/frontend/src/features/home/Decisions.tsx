import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Avatar } from "@close/ui/components/Avatar";
import { Icon } from "@close/ui/components/Icon";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { Segmented } from "@close/ui/components/Segmented";
import type { AttentionInbox } from "../closes/Attention";
import type { Obligation, Unopened } from "../closes/Morning";
import { workspaceRequest } from "../closes/workspace";
import { useDates } from "../../data/time";
import { dueWords } from "./Lists";

// What Home and Closes read in one snapshot, and the decision list both show: every close the reader can open,
// grouped by client with the most pressing first, drawn from the attention events and marked seen or snoozed there.
export type HomeCounts = {
  as_of: string;
  closes: number;
  can_add: boolean;
  can_review: boolean;
  signed_off: { done: number; total: number };
  decisions: { count: number; unread: number; team: number; snoozed: number };
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
export type View = keyof typeof views;

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
export function Decisions({ today, tick, snapshot, reopened, canReview, teamTotal, view, onView, onChange }: { today: string; tick: number; snapshot?: AttentionInbox; reopened: Reopened[]; canReview: boolean; teamTotal: number; view: View; onView: (view: View) => void; onChange: () => void }) {
  const dates = useDates();
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
  // A notice that needs no decision waits on no teammate, so it is not listed (or counted) as a teammate's.
  const team = (inbox?.groups ?? []).map((g) => ({ ...g, events: g.events.filter((e) => !e.yours && !e.info) })).filter((g) => g.events.length > 0);
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
              <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today, dates.calendarDay) : "No due date"} />
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
                  {[dueWords(g.due_on, today, dates.calendarDay), g.priority !== "normal" ? `${g.priority} priority` : "", g.owner === "mine" ? "Yours" : g.owner === "other" ? "With a teammate" : "Unassigned"].filter(Boolean).join(" · ")}
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
                    {!e.read && <span className="pill info">New</span>} {e.info && <span className="pill">Notice, no decision needed</span>} {dates.dayMonth(e.at)}
                    {e.snoozed_until && ` · snoozed until ${dates.dayMonth(e.snoozed_until)}`}
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
                      {dates.dayMonth(e.at)}
                      {e.snoozed_until && ` · snoozed until ${dates.dayMonth(e.snoozed_until)}`}
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
