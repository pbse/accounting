import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router";
import { Card, Cards } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { Icon } from "@close/ui/components/Icon";
import { clientPath } from "../../data/clients";
import { workspaceQuery } from "../../data/queries";
import { monthName, monthShort, useDates, useToday } from "../../data/time";
import { ClientForm, type Person, type RosterEntry } from "../closes/Roster";
import { workspaceRequest } from "../closes/workspace";

type Books = {
  source: "quickbooks" | "xero" | "exports";
  at: string;
  stale?: boolean;
};
type Month = {
  id: string;
  entity: string;
  period: string;
  signed_off: boolean;
  rechecked: boolean;
  unchecked?: boolean;
  due_on?: string;
  owned_by_me?: boolean;
  assigned?: boolean;
  books?: Books;
};
type Owed = { period: string; due_on?: string };
type QuickBooksConnection = {
  id: string;
  environment: "sandbox" | "production";
  state: string;
  realm: string;
  company_name: string;
  error: string;
};
type XeroConnection = {
  id: string;
  state: string;
  tenant_name: string;
  error: string;
};
type Profile = {
  currency: string;
  fiscal_year_start: number;
  state: "detected" | "none" | "unknown" | "conflict";
};
type ClientRead = {
  entity: string;
  // whole says the reader may see the client as a whole; without it they see only the months they can open, opened
  // under another of its names, and nothing else of the client.
  whole: boolean;
  can_manage: boolean;
  can_connect: boolean;
  roster: RosterEntry | null;
  template: { due_days: number | null; owner: string } | null;
  template_hidden: boolean;
  months: Month[];
  owed: Owed[];
  quickbooks: QuickBooksConnection[];
  xero: XeroConnection | null;
  profile: Profile | null;
  basis: {
    method: "accrual" | "cash" | "modified_cash";
    scope: "client" | "firm";
  } | null;
  requests: Request[];
  people?: Person[];
  people_unavailable?: string;
};
type Waiting = {
  items?: {
    id: string;
    close_id: string;
    period: string;
    source_name: string;
    kind?: string;
    proposal?: { kind?: string };
  }[];
};

const basisLabels = {
  cash: "cash basis",
  modified_cash: "modified cash basis",
  accrual: "accrual basis",
};
const sources = {
  quickbooks: "QuickBooks",
  xero: "Xero",
  exports: "Exports, as reported",
};
const fiscalMonth = (start: number) =>
  new Date(Date.UTC(2000, start - 1, 1)).toLocaleString("en-US", {
    month: "long",
    timeZone: "UTC",
  });
const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;
const live = (state: string) => state === "active" || state === "refreshing";

// A client's page (docs/books-sources.md, Client page): where the client stands in one sentence, then the cards that
// say how its books arrive, when it closes, what currency and year its books keep, and who works on it; then its
// months, newest first, and what it has been asked for. Its one action opens the month it owes, or the month in hand.
export default function ClientPage() {
  const { client: asked = "" } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const dates = useDates();
  const today = useToday();
  const read = useQuery(workspaceQuery<ClientRead>(clientPath(asked)));
  const client = read.data;
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const [dialog, setDialog] = useState<"" | "another" | "schedule">("");
  const reread = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  if (!client) {
    if (read.isPending) return <p role="status">Opening the client…</p>;
    return (
      <>
        <div className="ks-crumbs">
          <Link to="/dashboard/clients">Clients</Link>
          <span>/</span>
          <b>{asked}</b>
        </div>
        <p role="alert">{read.error?.message || "This client could not be opened."}</p>
      </>
    );
  }
  const name = client.entity;
  const months = client.months;
  const owed = [...client.owed].sort((a, b) => a.period.localeCompare(b.period));
  const open = months.filter((m) => !m.signed_off && !m.unchecked);
  const signed = months.filter((m) => m.signed_off);
  const active = client.roster?.active ?? true;
  async function openMonth(period: string) {
    setBusy(period);
    setError("");
    try {
      const saved = await workspaceRequest("", { entity: name, period });
      reread();
      navigate(`/dashboard/closes/${saved.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The month could not be opened.");
      setBusy("");
    }
  }

  // The sentence, in the order the work comes: a former client; a month open (the oldest, overdue first); the months it
  // owes that nobody opened; or how far it is signed off.
  const oldest = [...open].sort((a, b) => a.period.localeCompare(b.period))[0];
  const overdue = oldest?.due_on && oldest.due_on < today;
  const sentence = !client.whole
    ? `${name}: the months you can open.`
    : !active
      ? `${name} is no longer a client.`
      : oldest
        ? overdue
          ? `${name}’s ${monthName(oldest.period)} close is overdue.`
          : open.length === 1
            ? `${name}’s ${monthName(oldest.period)} close is open.`
            : `${name} has ${open.length} months open, from ${monthName(oldest.period)}.`
        : owed.length === 1
          ? `${name} owes ${monthName(owed[0].period)}.`
          : owed.length > 1
            ? `${name} owes ${owed.length} months, from ${monthName(owed[0].period)}.`
            : signed.length
              ? `${name} is signed off through ${monthName(signed[0].period)}.`
              : `${name} has no months yet.`;
  const ledger = books(client, dates.dayMonth);
  const roster = client.roster;
  const dueDays = roster?.due_days ?? client.template?.due_days ?? null;
  const context = !client.whole
    ? [`${n(months.length, "month")} opened under a name you are assigned to`, "the rest of the client is its team’s"]
    : [
        ledger.short,
        oldest && active && owed.length ? `${n(owed.length, "month")} to open` : "",
        roster ? (roster.active ? `closes each month from ${monthShort(roster.since)}` : "owes no new month") : "not on the roster",
        roster?.active ? (dueDays ? `due ${n(dueDays, "day")} after month-end` : roster.due_days === null && client.template_hidden ? "due as the firm’s template says" : "no due date") : "",
        months.length ? `${n(months.length, "month")} on record` : "",
      ];
  const contextLine = context.filter(Boolean).join(" · ");

  // The one action: the oldest month open, else the earliest month it owes.
  const inHand = active ? oldest : undefined;
  const next = active && !inHand && owed[0] ? owed[0] : null;
  // Connections and procedures are kept under the client's own name, so they are managed from its newest month opened
  // under that name (a month opened under another of its names shows neither).
  const latest = months.find((m) => m.entity.toLowerCase() === name.toLowerCase());

  // How each month is due and who holds it: the client's own terms, else the firm template's, said once.
  const people = client.people ?? [];
  const nameOf = (subject: string) => people.find((p) => p.subject === subject)?.name ?? subject;
  const owner = roster?.owner ?? (client.template ? client.template.owner : null);
  const scheduleLine = (() => {
    if (!roster) return `${name} is not on the roster, so no month is owed for it; its months are opened by hand.`;
    if (!roster.active) return `No longer a client: it owes no new month, and its months stay on record. ${roster.reason}`;
    const due = dueDays ? `due ${n(dueDays, "day")} after month-end` : roster.due_days === null && !client.template ? "due as the firm’s template says" : "with no due date";
    const holder = owner === null ? "Whoever the firm’s template names holds them." : owner ? `${nameOf(owner)} holds them.` : "Nobody holds them until someone takes one.";
    const follows = [roster.due_days === null ? "due date" : "", roster.owner === null ? "owner" : ""].filter(Boolean);
    const note = follows.length === 2 ? " Both follow the firm’s template." : follows.length ? ` The ${follows[0]} follows the firm’s template.` : "";
    return `Each month from ${monthName(roster.since)}, ${due}. ${holder}${note}`;
  })();

  const profile = client.profile ?? {
    currency: "USD",
    fiscal_year_start: 1,
    state: "none" as const,
  };
  const assumed = profile.state === "none" || profile.state === "unknown";
  const procedure = client.basis
    ? `Kept on the ${basisLabels[client.basis.method]}, by ${client.basis.scope === "client" ? `${name}’s own procedure` : "the firm’s procedure"}.`
    : "No accounting basis is approved yet.";
  const workers = people.length;
  const asks = client.requests;

  return (
    <>
      <div className="ks-crumbs">
        <Link to="/dashboard/clients">Clients</Link>
        <span>/</span>
        <b>{name}</b>
        <div className="ks-tools">
          {active && client.whole && (
            <button type="button" disabled={busy !== ""} onClick={() => setDialog("another")}>
              Open another month
            </button>
          )}
          {next ? (
            <button type="button" className="primary" disabled={busy !== ""} onClick={() => void openMonth(next.period)}>
              {busy === next.period ? "Opening…" : `Open ${monthName(next.period)}`}
            </button>
          ) : inHand ? (
            <Link className="button primary" to={`/dashboard/closes/${inHand.id}`}>
              Open {monthName(inHand.period)}
            </Link>
          ) : null}
        </div>
      </div>
      {read.error && <p role="alert">{read.error.message || "The client could not be refreshed."} Showing the last read.</p>}
      {error && dialog === "" && <p role="alert">{error}</p>}
      <div className="ks-lede">
        <h1>{sentence}</h1>
        <p>{contextLine.charAt(0).toUpperCase() + contextLine.slice(1)}</p>
      </div>

      {client.whole && (
        <Cards label={`About ${name}`}>
          <Card
            icon="book"
            tone={ledger.attention ? "wait" : "done"}
            title="Books"
            chip={<span className={ledger.chip[1]}>{ledger.chip[0]}</span>}
            action={
              latest ? (
                <Link className="button" to={`/dashboard/closes/${latest.id}?tab=${ledger.tab}#${ledger.anchor}`}>
                  {ledger.connected ? "Manage" : "Connect"} in {monthShort(latest.period)}
                </Link>
              ) : undefined
            }
          >
            {ledger.line}
            {latest ? "" : ` Open a month under ${name}’s own name to connect its ledger.`}
          </Card>
          <Card
            icon="history"
            tone={!roster ? "wait" : "done"}
            title="Monthly close"
            chip={!roster ? <span className="pill muted">Not on the roster</span> : roster.active ? <span className="pill">Active</span> : <span className="pill neutral">Former client</span>}
            action={
              client.can_manage ? (
                <button type="button" onClick={() => setDialog("schedule")}>
                  {roster ? "Change" : "Add to the roster"}
                </button>
              ) : undefined
            }
          >
            {scheduleLine}
          </Card>
          <Card
            icon="measure"
            tone={profile.state === "conflict" || assumed ? "wait" : "done"}
            title="Currency and year"
            chip={
              profile.state === "conflict" ? (
                <span className="pill danger">Ledgers disagree</span>
              ) : assumed ? (
                <span className="pill muted">Assumed</span>
              ) : (
                <span className="pill neutral">From the ledger</span>
              )
            }
            action={
              latest ? (
                <Link className="button" to={`/dashboard/closes/${latest.id}?tab=settings`}>
                  Procedures
                </Link>
              ) : undefined
            }
          >
            {profile.state === "conflict"
              ? `${name}’s connected ledgers report different currencies or fiscal years; disconnect the wrong one. ${procedure}`
              : `${profile.currency}, with a fiscal year starting in ${fiscalMonth(profile.fiscal_year_start)}${assumed ? (profile.state === "none" ? ", assumed until a ledger is connected" : ", assumed until the ledger’s currency is read") : ""}. ${procedure}`}
          </Card>
          {client.can_manage && (
            <Card
              icon="people"
              title="People"
              chip={client.people ? <span className="pill neutral">{n(workers, "person", "people")}</span> : undefined}
              action={
                <Link className="button" to="/dashboard/settings?tab=people">
                  Manage access
                </Link>
              }
            >
              {client.people_unavailable
                ? client.people_unavailable
                : workers
                  ? `${listed(people.map((p) => p.name))} can hold ${name}’s months.`
                  : `Nobody can hold ${name}’s months yet: assign a reviewer to it, or give a role every client.`}
            </Card>
          )}
        </Cards>
      )}

      <h2 className="ks-heading">Months</h2>
      <div className="ks-table ks-client-months" role="table" aria-label={`${name}’s months`}>
        <div className="ks-tr head" role="row">
          <span role="columnheader">Month</span>
          <span role="columnheader">Status</span>
          <span role="columnheader">Books</span>
          <span role="columnheader">Due</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {months.length === 0 && owed.length === 0 && (
          <div className="ks-tr" role="row">
            <p className="ks-empty-row" role="cell">
              No month yet. {active ? "Open its first month; the agent takes it from there." : ""}
            </p>
          </div>
        )}
        {active &&
          [...owed].reverse().map((o) => (
            <div className="ks-tr" role="row" key={o.period}>
              <span role="cell">{monthName(o.period)}</span>
              <span role="cell">
                <span className="pill muted">Not opened</span>
              </span>
              <span role="cell" className="ks-mut">
                —
              </span>
              <span role="cell" className="ks-mut">
                {o.due_on ? dates.calendarDate(o.due_on) : "—"}
              </span>
              <span role="cell">
                <button type="button" className="sm" disabled={busy !== ""} onClick={() => void openMonth(o.period)}>
                  {busy === o.period ? "Opening…" : "Open"}
                </button>
              </span>
            </div>
          ))}
        {months.map((m) => (
          <div className="ks-tr linked" role="row" key={m.id}>
            <span role="cell">
              <Link className="ks-rowlink" to={`/dashboard/closes/${m.id}`}>
                {monthName(m.period)}
              </Link>
              {m.entity.toLowerCase() !== name.toLowerCase() && <small className="ks-mut"> · opened as {m.entity}</small>}
            </span>
            <span role="cell">{monthState(m, today)}</span>
            <span role="cell" className="ks-mut">
              {m.books ? `${sources[m.books.source]} · ${dates.dayMonth(m.books.at)}${m.books.stale ? " · out of date" : ""}` : "—"}
            </span>
            <span role="cell" className="ks-mut">
              {m.due_on ? dates.calendarDate(m.due_on) : "—"}
            </span>
            <Icon name="arrow" className="ks-go" />
          </div>
        ))}
      </div>

      {asks.length > 0 && (
        <>
          <h2 className="ks-heading">Waiting on {name}</h2>
          <div className="ks-table ks-client-asks" role="table" aria-label={`Requests waiting on ${name}`}>
            {asks.map((item) => (
              <div className="ks-tr linked" role="row" key={item.id}>
                <span role="cell">
                  <Link className="ks-rowlink" to={`/dashboard/closes/${item.close_id}?tab=requests`}>
                    Request #{item.id} to {item.recipient}
                  </Link>
                </span>
                <span role="cell" className="ks-mut">
                  {monthName(item.period)} · {n(item.items, "item")} · sent {dates.dayMonth(item.sent_at)}
                </span>
                <Icon name="arrow" className="ks-go" />
              </div>
            ))}
          </div>
        </>
      )}

      <Dialog
        open={dialog === "another"}
        onClose={() => (setDialog(""), setError(""))}
        title="Open another month"
        description={`Open any month of ${name}’s, earlier or later; one already open is opened as it is.`}
      >
        <form
          onSubmit={(event: FormEvent<HTMLFormElement>) => {
            event.preventDefault();
            void openMonth(String(new FormData(event.currentTarget).get("period")));
          }}
        >
          {error && <p role="alert">{error}</p>}
          <label>
            Month
            <input name="period" type="month" required defaultValue={today.slice(0, 7)} />
          </label>
          <DialogActions>
            <button type="button" onClick={() => (setDialog(""), setError(""))}>
              Cancel
            </button>
            <button className="primary" disabled={busy !== ""}>
              {busy ? "Opening…" : "Open the month"}
            </button>
          </DialogActions>
        </form>
      </Dialog>
      {client.can_manage && (
        <Dialog
          open={dialog === "schedule"}
          onClose={() => setDialog("")}
          title={roster ? `${name}’s monthly close` : `Add ${name} to the roster`}
          description="Whether the client owes a close each month, from when, and its own due date and owner."
        >
          {client.people_unavailable && <p role="alert">{client.people_unavailable}</p>}
          <ClientForm key={roster?.rev ?? "new"} client={roster ?? undefined} named={name} people={people} onSaved={() => (setDialog(""), reread())} onCancel={() => (setDialog(""), reread())} />
        </Dialog>
      )}
    </>
  );
}

// A month's state in a word: open (overdue past its due date), signed off, or a sign-off that is not rechecked.
function monthState(m: Month, today: string) {
  if (m.unchecked) return <span className="pill danger">Sign-off not checked</span>;
  if (m.signed_off && m.rechecked === false) return <span className="pill neutral">Signed off, not rechecked</span>;
  if (m.signed_off) return <span className="pill">Signed off</span>;
  if (m.due_on && m.due_on < today) return <span className="pill danger">Overdue</span>;
  return <span className="pill neutral">Open</span>;
}

// How the client's books arrive, from its connections (the QuickBooks one its books are read from first, then Xero),
// and the newest month's books: one line, a chip, and where the connection is managed today.
function books(client: ClientRead, day: (value: string) => string) {
  const qbo = client.quickbooks[0];
  const xero = client.xero;
  const newest = client.months.find((m) => m.books);
  const latest = newest?.books
    ? newest.books.source === "exports"
      ? ` ${monthName(newest.period)}’s exports were labeled ${day(newest.books.at)}, as reported.`
      : ` ${monthName(newest.period)}’s books were captured ${day(newest.books.at)}${newest.books.stale ? " and are out of date" : ""}.`
    : "";
  const state = (s: string, confirm: string): [string, string] =>
    live(s)
      ? ["Connected", "pill"]
      : s === "reconnect_required"
        ? ["Reconnect", "pill danger"]
        : s === "pending_confirmation" || s === "choose_tenant"
          ? [confirm, "pill muted"]
          : ["Connecting", "pill neutral"];
  if (qbo) {
    const production = qbo.environment === "production";
    return {
      short: production ? "QuickBooks Online" : "QuickBooks sandbox",
      line: `Read from QuickBooks${production ? "" : " (sandbox)"}${qbo.company_name ? `, ${qbo.company_name}` : ""}.${qbo.error ? ` ${qbo.error}` : ""}${latest}`,
      chip: state(qbo.state, "Confirm the company"),
      attention: !live(qbo.state),
      connected: true,
      tab: production ? "settings" : "evidence",
      anchor: production ? "quickbooks-production" : "quickbooks-ledger",
    };
  }
  if (xero) {
    return {
      short: "Xero",
      line: `Read from Xero${xero.tenant_name ? `, ${xero.tenant_name}` : ""}.${xero.error ? ` ${xero.error}` : ""}${latest}`,
      chip: state(xero.state, "Choose the organisation"),
      attention: !live(xero.state),
      connected: true,
      tab: "evidence",
      anchor: "xero",
    };
  }
  return {
    short: newest?.books?.source === "exports" ? "books from exports" : "no ledger connected",
    line: `No ledger is connected. Connect QuickBooks or Xero, or label the exports a month uses.${latest}`,
    chip: ["Not connected", "pill neutral"] as [string, string],
    attention: false,
    connected: false,
    tab: "evidence",
    anchor: "quickbooks-ledger",
  };
}

// Names joined as a sentence lists them: "Ann", "Ann and Cy", "Ann, Bea and Cy".
function listed(names: string[]) {
  return names.length <= 1 ? (names[0] ?? "") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}
