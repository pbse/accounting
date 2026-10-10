import { Link, useSearchParams } from "react-router";
import { useState } from "react";
import { post, useResource, when } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "../components/ActionForm";
import { PageHeader } from "@close/ui/components/PageHeader";

// Filters kept in the URL, so a view can be shared and reloaded; changing one goes back to the first page.
function useFilters(names: string[]) {
  const [params, setParams] = useSearchParams();
  const offset = Number(params.get("offset") ?? 0) || 0;
  const setAll = (values: Record<string, string>) => {
    const next = new URLSearchParams(params);
    for (const [name, value] of Object.entries(values)) {
      if (value.trim()) next.set(name, value.trim());
      else next.delete(name);
    }
    next.delete("offset");
    setParams(next);
  };
  const set = (name: string, value: string) => setAll({ [name]: value });
  // A form of text filters applies them together on submit, not on every keystroke.
  const submit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setAll(Object.fromEntries([...new FormData(event.currentTarget)].map(([k, v]) => [k, String(v)])));
  };
  const page = (value: number) => {
    const next = new URLSearchParams(params);
    if (value) next.set("offset", String(value));
    else next.delete("offset");
    setParams(next);
  };
  const query = new URLSearchParams();
  for (const name of [...names, "offset"]) if (params.get(name)) query.set(name, params.get(name)!);
  return { params, offset, set, setAll, submit, page, query: query.size ? `?${query}` : "" };
}

function Pager({ offset, more, next, onPage }: { offset: number; more: boolean; next: number; onPage: (offset: number) => void }) {
  if (!offset && !more) return null;
  return (
    <p className="title-actions">
      {offset > 0 && <button onClick={() => onPage(0)}>First page</button>}
      {more && <button onClick={() => onPage(next)}>More</button>}
    </p>
  );
}

const age = (minutes: number) => (minutes < 120 ? `${minutes} min` : minutes < 2880 ? `${Math.round(minutes / 60)} h` : `${Math.round(minutes / 1440)} days`);

type Kind = { kind: string; items: number; firms: number; oldest_minutes: number };
type Item = { organization: string; firm_name: string; enabled: boolean; kind: string; close_id: string; item_id: string; state: string; age_minutes: number };

// An email whose outcome is unknown, or a send that stopped, settled from the firm's provider records; also while the
// firm is suspended, as recovery needs.
function SettleEmail({ item, onDone }: { item: Item; onDone: () => void }) {
  const [outcome, setOutcome] = useState("accepted");
  const [provider, setProvider] = useState("");
  const [evidence, setEvidence] = useState("");
  const operate = usePermissions().includes("platform:operate");
  return (
    <details>
      <summary>Settle</summary>
      <ActionForm label="Settle email" allowed={operate} minLength={10} onSubmit={async (reason) => {
        await post(`/v1/admin/firms/${encodeURIComponent(item.organization)}/emails/${encodeURIComponent(item.item_id)}/settle`, { expected_state: item.state, outcome, provider_id: outcome === "accepted" ? provider.trim() : "", evidence: evidence.trim(), reason });
        onDone();
      }}>
        <label>Outcome<select value={outcome} onChange={(e) => setOutcome(e.target.value)}><option value="accepted">Accepted: the provider has it</option>{item.state === "unknown" && <option value="failed">Failed: the provider has no such email</option>}</select></label>
        <label>Provider message ID<input value={provider} maxLength={200} onChange={(e) => setProvider(e.target.value)} /></label>
        <label>What the provider's records show<textarea value={evidence} minLength={5} maxLength={2000} required onChange={(e) => setEvidence(e.target.value)} /></label>
      </ActionForm>
    </details>
  );
}

export function Jobs() {
  const f = useFilters(["kind", "firm"]);
  const jobs = useResource<{ kinds: Kind[]; items: Item[]; has_more: boolean; next_offset: number }>(`/v1/admin/jobs${f.query}`);
  return (
    <>
      <PageHeader
        title="Stuck work"
      >
        Background work that failed or has waited too long, across firms: what each firm's operations screen shows. Firms act on it from there.
      </PageHeader>
      <form className="filters" onSubmit={f.submit}>
        <label>Firm ID<input name="firm" key={f.params.get("firm")} defaultValue={f.params.get("firm") ?? ""} placeholder="Any" /></label>
        <button>Filter</button>
        {f.params.get("kind") && <button type="button" onClick={() => f.set("kind", "")}>All kinds</button>}
      </form>
      {jobs.error && <p role="alert">{jobs.error}</p>}
      {!jobs.data && !jobs.error && <p role="status">Loading…</p>}
      {jobs.data && (
        <>
          <section>
            <h2>By kind</h2>
            {jobs.data.kinds.length === 0 ? <p>Nothing is stuck or failed.</p> : (
              <table>
                <thead><tr><th>Kind</th><th>Items</th><th>Firms</th><th>Oldest</th></tr></thead>
                <tbody>
                  {jobs.data.kinds.map((k) => (
                    <tr key={k.kind}>
                      <td><button className={f.params.get("kind") === k.kind ? "link selected" : "link"} onClick={() => f.set("kind", k.kind)}>{k.kind.replaceAll("_", " ")}</button></td>
                      <td>{k.items}</td>
                      <td>{k.firms}</td>
                      <td>{age(k.oldest_minutes)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
          <section>
            <h2>Items, oldest first</h2>
            {jobs.data.items.length === 0 ? <p>None on this page.</p> : (
              <table>
                <thead><tr><th>Firm</th><th>Kind</th><th>Close</th><th>Item</th><th>State</th><th>Age</th></tr></thead>
                <tbody>
                  {jobs.data.items.map((i) => (
                    <tr key={`${i.kind}-${i.item_id}`}>
                      <td><Link to={`/firms/${encodeURIComponent(i.organization)}`}>{i.firm_name}</Link>{!i.enabled && <small> suspended</small>}</td>
                      <td>{i.kind.replaceAll("_", " ")}</td>
                      <td>{i.close_id}</td>
                      <td>{i.item_id}{i.kind === "email" && <SettleEmail item={i} onDone={jobs.reload} />}</td>
                      <td>{i.state}</td>
                      <td>{age(i.age_minutes)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <Pager offset={f.offset} more={jobs.data.has_more} next={jobs.data.next_offset} onPage={f.page} />
          </section>
        </>
      )}
    </>
  );
}

type Count = { provider: string; state: string; connections: number };
type Connection = { provider: string; organization: string; firm_name: string; close_id: string; state: string; mode: string; connected_at: string; updated_at: string; last_capture: string | null };
const providers = ["", "quickbooks", "xero", "stripe", "email"];
const tone: Record<string, string> = { active: "ok", reconnect_required: "danger", disconnected: "", unconfirmed: "warn" };

// The month-books record (docs/books-sources.md, Migration): how many month-end captures have their books version, and
// whether the readers still agree with every version written. A reader moves to the record only while this agrees.
type BooksRecord = { captures: number; versions: number; pending: number; unreadable: number; parity: { checked: number; mismatched: string[] } };

function MonthBooks() {
  const books = useResource<BooksRecord>("/v1/admin/books");
  return (
    <section>
      <h2>Month-books record</h2>
      {books.error && <p role="alert">{books.error}</p>}
      {!books.data && !books.error && <p role="status">Loading…</p>}
      {books.data && (
        <>
          <p>
            {books.data.versions} of {books.data.captures} month-end captures have their books version{books.data.pending ? `; ${books.data.pending} are waiting for the worker` : ""}. {books.data.unreadable} are kept as unreadable, as the readers find
            them.
          </p>
          <p>
            <span className={`status ${books.data.parity.mismatched.length ? "danger" : "ok"}`}>
              {books.data.parity.mismatched.length ? `${books.data.parity.mismatched.length} disagree` : "The readers agree"}
            </span>{" "}
            with {books.data.parity.checked} versions checked.
          </p>
          {books.data.parity.mismatched.length > 0 && (
            <ul>
              {books.data.parity.mismatched.map((m) => (
                <li key={m}>{m}</li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

export function Connections() {
  const f = useFilters(["provider", "state", "firm"]);
  const connections = useResource<{ counts: Count[]; connections: Connection[]; has_more: boolean; next_offset: number }>(`/v1/admin/connections${f.query}`);
  return (
    <>
      <PageHeader
        title="Connections"
      >
        QuickBooks, Xero, Stripe and email sending across firms, and when each last brought data in. Reconnecting stays with the firm.
      </PageHeader>
      <form className="filters" onSubmit={f.submit}>
        <label>Provider
          <select name="provider" key={f.params.get("provider")} defaultValue={f.params.get("provider") ?? ""}>
            {providers.map((p) => <option key={p} value={p}>{p || "Any"}</option>)}
          </select>
        </label>
        <label>State<input name="state" key={f.params.get("state")} defaultValue={f.params.get("state") ?? ""} placeholder="Any" /></label>
        <label>Firm ID<input name="firm" key={f.params.get("firm")} defaultValue={f.params.get("firm") ?? ""} placeholder="Any" /></label>
        <button>Filter</button>
      </form>
      {connections.error && <p role="alert">{connections.error}</p>}
      {!connections.data && !connections.error && <p role="status">Loading…</p>}
      {connections.data && (
        <>
          <section>
            <h2>By provider and state</h2>
            {connections.data.counts.length === 0 ? <p>No connection yet.</p> : (
              <table>
                <thead><tr><th>Provider</th><th>State</th><th>Connections</th></tr></thead>
                <tbody>
                  {connections.data.counts.map((c) => (
                    <tr key={`${c.provider}-${c.state}`}>
                      <td>{c.provider}</td>
                      <td><button className="link" onClick={() => f.setAll({ provider: c.provider, state: c.state })}>{c.state.replaceAll("_", " ")}</button></td>
                      <td>{c.connections}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
          <section>
            <h2>Connections, reconnects first</h2>
            {connections.data.connections.length === 0 ? <p>None match.</p> : (
              <table>
                <thead><tr><th>Firm</th><th>Provider</th><th>Close</th><th>State</th><th>Connected</th><th>Changed</th><th>Last data</th></tr></thead>
                <tbody>
                  {connections.data.connections.map((c) => (
                    <tr key={`${c.provider}-${c.organization}-${c.close_id}-${c.connected_at}`}>
                      <td><Link to={`/firms/${encodeURIComponent(c.organization)}`}>{c.firm_name}</Link></td>
                      <td>{c.provider}{c.mode && <small> {c.mode}</small>}</td>
                      <td>{c.close_id || "—"}</td>
                      <td><span className={`status ${tone[c.state] ?? ""}`}>{c.state.replaceAll("_", " ")}</span></td>
                      <td>{when(c.connected_at)}</td>
                      <td>{when(c.updated_at)}</td>
                      <td>{when(c.last_capture)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <Pager offset={f.offset} more={connections.data.has_more} next={connections.data.next_offset} onPage={f.page} />
          </section>
        </>
      )}
      <MonthBooks />
    </>
  );
}
