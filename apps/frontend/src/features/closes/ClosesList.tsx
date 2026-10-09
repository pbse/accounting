import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { monthShort, useToday } from "../../data/time";
import { Link, useNavigate, useSearchParams } from "react-router";
import { authKitClientId } from "../auth/Auth";
import { Icon } from "@close/ui/components/Icon";
import { PageHeader } from "@close/ui/components/PageHeader";
import { EntityCell, RowList } from "@close/ui/components/RowList";
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

// Clients: every client the reader can work on, with the months it owes that nobody opened (one tap opens one) and
// its open closes. A firm administrator adds a client by name; nothing else is asked.
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
      // The form stays, with what was typed, so the person corrects it rather than starting over.
      setAdding(true);
      setError(e instanceof Error ? e.message : "The client could not be added.");
    } finally {
      setBusy("");
    }
  }
  // Empty only when the roster was read: a failed read says nothing about whether there are clients.
  const empty = !loading && !!roster && rows.size === 0 && !query;
  return (
    <>
      <PageHeader
        icon="clients"
        title="Clients"
        actions={
          <div className="title-actions">
            <form
              role="search"
              onSubmit={(event) => {
                event.preventDefault();
                const next = String(new FormData(event.currentTarget).get("q") ?? "").trim();
                setParams(next ? { q: next } : {});
              }}
            >
              <input name="q" aria-label="Search clients" placeholder="Client or YYYY-MM" maxLength={200} defaultValue={query} key={query} />
            </form>
            {roster?.can_manage && (
              <button type="button" className="primary" onClick={() => setAdding((v) => !v)}>
                Add a client
              </button>
            )}
          </div>
        }
      >
        Each client's months: owed, open or signed off.
      </PageHeader>
      {error && <p role="alert">{error}</p>}
      {isEnrollmentError(error) && <FirmEnrollment onEnrolled={reread} />}
      {roster?.can_manage && (adding || empty) && (
        <form className="add-client" onSubmit={add} aria-label="Add a client">
          <label>
            Client name
            <input name="entity" required maxLength={200} autoFocus placeholder="Acme Services" />
          </label>
          <label>
            First month to close
            <input name="since" type="month" defaultValue={thisMonth} />
          </label>
          <button className="primary" disabled={busy === "add"}>
            {busy === "add" ? "Adding…" : "Add client"}
          </button>
          {!empty && (
            <button type="button" disabled={busy === "add"} onClick={() => setAdding(false)}>
              Cancel
            </button>
          )}
        </form>
      )}
      <RowList label="Clients">
        {loading && <p role="status" className="empty">Loading clients…</p>}
        {empty && <p className="empty">{roster?.can_manage ? "No clients yet. Add your first client above; the agent takes it from there." : "No client is assigned to you yet."}</p>}
        {!loading && query && shown.length === 0 && <p className="empty">No client or month matches “{query}”.</p>}
        {shown.map((r) => (
          <div className="row client-row" key={r.entity}>
            <EntityCell
              name={r.entity}
              title={r.entity}
              detail={[
                r.active === false ? "No longer a client" : r.since ? `Closes each month from ${monthName(r.since)}` : "",
                r.closes.filter((c) => !c.signed_off && !c.unchecked).length ? `${r.closes.filter((c) => !c.signed_off && !c.unchecked).length} open` : "",
                r.closes.filter((c) => c.unchecked).length ? `${r.closes.filter((c) => c.unchecked).length} whose sign-off could not be checked` : "",
                r.closes.filter((c) => c.signed_off && c.rechecked !== false).length ? `${r.closes.filter((c) => c.signed_off && c.rechecked !== false).length} signed off` : "",
                r.closes.filter((c) => c.signed_off && c.rechecked === false).length ? `${r.closes.filter((c) => c.signed_off && c.rechecked === false).length} older, sign-off not rechecked here` : "",
              ]
                .filter(Boolean)
                .join(" · ")}
            />
            <span className="client-months">
              {r.owed.map((period) => (
                <button type="button" className="chip owed" key={period} disabled={busy !== ""} onClick={() => void open(r.entity, period)}>
                  {busy === `${r.entity}:${period}` ? "Opening…" : `Open ${monthName(period)}`}
                </button>
              ))}
              {r.closes.map((c) => (
                <Link
                  className={c.signed_off && c.rechecked !== false ? "chip signed" : "chip"}
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
            {r.active !== false && (
            <details className="open-month">
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
            )}
          </div>
        ))}
      </RowList>
      {more && <p className="note">Showing the 100 most recent closes. Search by client or YYYY-MM to find older ones.</p>}
    </>
  );
}
