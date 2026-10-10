import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { monthShort, useToday } from "../../data/time";
import { Link, useNavigate, useSearchParams } from "react-router";
import { authKitClientId } from "../auth/Auth";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { Icon } from "@close/ui/components/Icon";
import { Mark } from "@close/ui/components/Mark";
import { PageHeader } from "@close/ui/components/PageHeader";
import { FirmEnrollment } from "./Enterprise";
import { PlanChips } from "./Planning";
import { workspaceRequest } from "./workspace";
import type { Close } from "./WorkpaperView";

export function isEnrollmentError(message: string) {
  return !!authKitClientId && message.includes("firm or membership");
}

type RosterClient = { entity: string; active: boolean; since: string; last_period: string; owed: string[]; closes: { id: string; entity: string; period: string; signed_off: boolean; rechecked: boolean; unchecked?: boolean; due_on?: string; priority?: string; assigned?: boolean; owned_by_me?: boolean }[] };
type Roster = { clients: RosterClient[]; can_manage: boolean };
type ListedClose = Close & { client?: string; signed_off?: boolean; rechecked?: boolean; unchecked?: boolean };
type ClientRow = { entity: string; active?: boolean; since?: string; closes: ListedClose[]; owed: string[] };

const monthName = monthShort;

// Clients, the records page (docs/design-system.md, A records page): every client the reader can work on, one row
// each with its months (owed and not opened, open, signed off), the search in the title row and Add client as its one
// action, made in a dialog. A firm administrator adds a client by name; nothing else is asked.
export default function ClosesList() {
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const query = params.get("q") ?? "";
  const queryClient = useQueryClient();
  // This month, in the person's own time zone, by the render's clock.
  const thisMonth = useToday().slice(0, 7);
  // The recent closes, and with a search also those matching it (older ones among them), for clients the roster
  // does not list. The roster lists every client a firm administrator can see, and for a member the clients assigned
  // to them, each with the months it owes that nobody opened. All are read with the page.
  const recent = useQuery(workspaceQuery<{ closes?: ListedClose[]; has_more?: boolean }>(""));
  const searched = useQuery({ ...workspaceQuery<{ closes?: ListedClose[]; has_more?: boolean }>(`?q=${encodeURIComponent(query)}`), enabled: !!query });
  const rosterRead = useQuery(workspaceQuery<Roster>("/roster"));
  const roster = rosterRead.data;
  const matched = query ? (searched.data?.closes ?? []) : [];
  const seen = new Set<string>();
  const closes = [...(recent.data?.closes ?? []), ...matched].filter((c) => !seen.has(c.id) && seen.add(c.id));
  // The closes the server matched to the search (a confirmed alias among the ways it matches), kept whatever the
  // local names say.
  const found = new Set(matched.map((c) => c.id));
  const more = !!(query ? searched.data?.has_more : recent.data?.has_more);
  const loading = recent.isPending || rosterRead.isPending || (!!query && searched.isPending);
  const readError = recent.error ?? rosterRead.error ?? (query ? searched.error : null);
  const [actionError, setError] = useState("");
  // A failed action belongs to the search it was made in; a new search starts without it.
  const [errorFor, setErrorFor] = useState(query);
  if (errorFor !== query) {
    setErrorFor(query);
    setError("");
  }
  const error = actionError || (readError ? readError.message || "Could not open your clients." : "");
  const [busy, setBusy] = useState("");
  const [adding, setAdding] = useState(false);
  const reread = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  // One row per client: the roster names each by its own name, a close by the name it was opened with (its `client`
  // is the own name a confirmed alias resolves to), and an owed month by the own name.
  const rows = new Map<string, ClientRow>();
  const row = (entity: string, key = entity.toLowerCase()) => {
    let found = rows.get(key);
    if (!found) rows.set(key, (found = { entity, closes: [], owed: [] }));
    return found;
  };
  // The roster carries each client's whole history (every close under any of its names) and which months it can open:
  // every month from its first through this one that has no close and was not deleted as done. The recent and searched
  // closes add only clients the roster does not list.
  for (const c of roster?.clients ?? []) Object.assign(row(c.entity), { active: c.active, since: c.since, owed: [...c.owed], closes: c.closes.map((cl) => ({ ...cl, client: c.entity.toLowerCase() })) });
  // The recent and searched closes carry each close's due date, priority and owner; a close the roster also lists
  // takes them from here.
  for (const c of closes) {
    const r = row(c.entity, c.client || c.entity.toLowerCase());
    // The list adds what the roster's history lacks: a close of a client off the roster, and each close's planning.
    // Where both read a close, the roster's sign-off state stands (one read, one moment); the list's is used only
    // for a close the roster does not have.
    const known = r.closes.find((cl) => cl.id === c.id);
    if (known) Object.assign(known, { due_on: c.due_on, priority: c.priority, assigned: c.assigned, owned_by_me: c.owned_by_me, can_assign: c.can_assign, can_take_over: c.can_take_over, handed: c.handed });
    else r.closes.push({ ...c, rechecked: c.rechecked === true });
  }
  const needle = query.trim().toLowerCase();
  // A search matches a client's own name, any name its closes were opened with, or a month.
  const shown = [...rows.values()]
    .filter((r) => !needle || r.entity.toLowerCase().includes(needle) || r.closes.some((c) => found.has(c.id) || c.entity.toLowerCase().includes(needle) || c.period === needle) || r.owed.includes(needle))
    .sort((a, b) => Number(b.active ?? true) - Number(a.active ?? true) || a.entity.localeCompare(b.entity));
  async function open(entity: string, period: string) {
    setBusy(`${entity}:${period}`);
    setError("");
    try {
      const saved = await workspaceRequest("", { entity, period });
      navigate(`/dashboard/closes/${saved.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The month could not be opened.");
      setBusy("");
    }
  }
  async function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const entity = String(data.get("entity") ?? "").trim();
    setBusy("add");
    setError("");
    try {
      await workspaceRequest("/roster", { entity, active: true, since: String(data.get("since") || thisMonth), due_days: null, owner: null, reason: "", rev: "" });
      setAdding(false);
      reread();
    } catch (e) {
      // The dialog stays, with what was typed, so the person corrects it rather than starting over.
      setAdding(true);
      setError(e instanceof Error ? e.message : "The client could not be added.");
    } finally {
      setBusy("");
    }
  }
  // Empty only when the roster was read: a failed read says nothing about whether there are clients.
  const empty = !loading && !!roster && rows.size === 0 && !query;
  const open_ = (r: ClientRow) => r.closes.filter((c) => !c.signed_off && !c.unchecked).length;
  const waiting = shown.filter((r) => r.owed.length > 0 || open_(r) > 0).length;
  const context = loading ? "Reading your clients…" : roster ? `${n(rows.size, "client")}${waiting ? ` · ${waiting} with a month open or owed` : ""}` : "";
  // A client's state in a word: former, owing a month, a month open, or all signed off.
  const state = (r: ClientRow) => {
    if (r.active === false) return <span className="pill neutral">Former client</span>;
    if (r.owed.length) return <span className="pill muted">{n(r.owed.length, "month")} to open</span>;
    if (open_(r)) return <span className="pill muted">{n(open_(r), "month")} open</span>;
    if (r.closes.some((c) => c.unchecked)) return <span className="pill danger">Sign-off not checked</span>;
    if (r.closes.some((c) => c.signed_off && c.rechecked === false)) return <span className="pill neutral">Older sign-off, not rechecked</span>;
    if (r.closes.length) return <span className="pill">Signed off</span>;
    return <span className="pill neutral">New</span>;
  };
  return (
    <>
      <PageHeader
        title="Clients"
        actions={
          <div className="ks-tools">
            <form
              role="search"
              className="ks-search"
              onSubmit={(event) => {
                event.preventDefault();
                const next = String(new FormData(event.currentTarget).get("q") ?? "").trim();
                setParams(next ? { q: next } : {});
              }}
            >
              <Icon name="search" />
              <input name="q" aria-label="Search clients" placeholder="Client or YYYY-MM" maxLength={200} defaultValue={query} key={query} />
            </form>
            {roster?.can_manage && (
              <button type="button" className="primary" onClick={() => setAdding(true)}>
                <Icon name="plus" /> Add client
              </button>
            )}
          </div>
        }
      >
        {context}
      </PageHeader>
      {error && !adding && <p role="alert">{error}</p>}
      {isEnrollmentError(error) && <FirmEnrollment onEnrolled={reread} />}
      <Dialog open={adding} onClose={() => setAdding(false)} title="Add a client" description="Name the client and its first month to close. The agent takes it from there.">
        <form onSubmit={add} aria-label="Add a client">
          {error && <p role="alert">{error}</p>}
          <label>
            Client name
            <input name="entity" required maxLength={200} autoFocus placeholder="Acme Services" />
          </label>
          <label>
            First month to close
            <input name="since" type="month" defaultValue={thisMonth} />
          </label>
          <DialogActions>
            <button type="button" disabled={busy === "add"} onClick={() => setAdding(false)}>
              Cancel
            </button>
            <button className="primary" disabled={busy === "add"}>
              {busy === "add" ? "Adding…" : "Add client"}
            </button>
          </DialogActions>
        </form>
      </Dialog>
      <div className="ks-table ks-clients" role="table" aria-label="Clients">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Client</span>
          <span role="columnheader">Months</span>
          <span role="columnheader">Status</span>
          <span role="columnheader">
            <span className="visually-hidden">Open another month</span>
          </span>
        </div>
        {loading && (
          <div className="ks-tr" role="row">
            <p className="ks-empty-row" role="cell" aria-live="polite">
              Loading clients…
            </p>
          </div>
        )}
        {empty && (
          <div className="ks-tr" role="row">
            <p className="ks-empty-row" role="cell">{roster?.can_manage ? "No clients yet. Add your first client; the agent takes it from there." : "No client is assigned to you yet."}</p>
          </div>
        )}
        {!loading && query && shown.length === 0 && (
          <div className="ks-tr" role="row">
            <p className="ks-empty-row" role="cell">No client or month matches “{query}”.</p>
          </div>
        )}
        {shown.map((r) => (
          <div className="ks-tr" role="row" key={r.entity}>
            <span className="ks-who" role="cell">
              <Mark label={r.entity} />
              <span title={r.active === false ? "No longer a client" : r.since ? `Closes each month from ${monthName(r.since)}` : undefined}>{r.entity}</span>
            </span>
            <span className="ks-months" role="cell">
              {r.owed.map((period) => (
                <button type="button" className="ks-month owed" key={period} disabled={busy !== ""} onClick={() => void open(r.entity, period)}>
                  {busy === `${r.entity}:${period}` ? "Opening…" : `Open ${monthName(period)}`}
                </button>
              ))}
              {r.closes.map((c) => (
                <Link
                  className={c.signed_off && c.rechecked !== false ? "ks-month signed" : "ks-month"}
                  key={c.id}
                  to={`/dashboard/closes/${c.id}`}
                  title={c.signed_off && c.rechecked === false ? "A sign-off is recorded but not rechecked here; open the close to see whether it still stands" : undefined}
                >
                  {c.signed_off && c.rechecked !== false && <Icon name="review" size={12} />}
                  {monthName(c.period)}
                  {c.unchecked && <span className="pill danger">not checked</span>}
                  {/* Due and owner chips for open work only: a recorded or unchecked sign-off is not called overdue. */}
                  {!c.signed_off && !c.unchecked && <PlanChips plan={c} />}
                </Link>
              ))}
            </span>
            <span role="cell">{state(r)}</span>
            {r.active !== false ? (
              <details className="ks-another" role="cell">
                <summary>Another month</summary>
                <form
                  onSubmit={(event) => {
                    event.preventDefault();
                    void open(r.entity, String(new FormData(event.currentTarget).get("period")));
                  }}
                >
                  <input name="period" type="month" required aria-label={`Month to open for ${r.entity}`} />
                  <button disabled={busy !== ""}>Open</button>
                </form>
              </details>
            ) : (
              <span role="cell" />
            )}
          </div>
        ))}
      </div>
      {more && <p className="note">Showing the 100 most recent closes. Search by client or YYYY-MM to find older ones.</p>}
    </>
  );
}

const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;
