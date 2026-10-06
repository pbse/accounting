import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Template = { due_days: number | null; owner: string; reason: string; actor: string; at: string } | null;
type Client = { entity: string; active: boolean; since: string; due_days: number | null; owner: string | null; reason: string; actor: string; at: string; last_period: string; owed: string[] };
type Person = { subject: string; name: string; firm_wide: boolean; clients: string[] };
type Roster = { template: Template; clients: Client[]; can_manage: boolean; people?: Person[]; people_unavailable?: string };

const days = Array.from({ length: 60 }, (_, i) => i + 1);
const endedMonth = () => {
  const now = new Date();
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - 1, 1)).toISOString().slice(0, 7);
};

// The firm's clients and its monthly close template: every active client owes a close each month, and a month nobody
// opened shows in Work until it is opened. Each month's close takes the template's due date and owner, unless the client's
// own entry overrides them. Firm administrators change both.
export function ClientRoster() {
  const [roster, setRoster] = useState<Roster>();
  const [error, setError] = useState("");
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/roster")
      .then((data: Roster) => live && (setRoster(data), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "The client roster is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!roster) return error ? <section aria-label="Clients and monthly closes"><h2>Clients and monthly closes</h2><p role="alert">{error}</p></section> : null;
  const people = roster.people ?? [];
  const name = (subject: string) => people.find((p) => p.subject === subject)?.name ?? subject;
  const t = roster.template;
  return (
    <section aria-label="Clients and monthly closes">
      <h2>Clients and monthly closes</h2>
      <p>
        Every active client owes a close each month. A month nobody has opened shows in Work until it is opened, and opening it gives the close{" "}
        {t ? (
          <>
            {t.due_days ? `a due date ${t.due_days} ${t.due_days === 1 ? "day" : "days"} after the month ends` : "no due date"} and {t.owner ? <>{name(t.owner)} as its owner</> : "no owner"}
          </>
        ) : (
          "no due date or owner until the firm sets its monthly template"
        )}
        , unless the client's own entry says otherwise.
      </p>
      {error && <p role="alert">{error} Showing the last roster read.</p>}
      {roster.people_unavailable && <p role="alert">{roster.people_unavailable}</p>}
      {roster.can_manage && <TemplateForm key={t?.at ?? "none"} template={t} people={people.filter((p) => p.firm_wide)} onSaved={() => setRefresh((n) => n + 1)} />}
      {roster.clients.length === 0 ? (
        <p className="empty">No client is on the roster yet. A client joins when it is taken over, or when a firm administrator adds it.</p>
      ) : (
        <ul>
          {roster.clients.map((c) => (
            <li key={c.entity}>
              <strong>{c.entity}</strong> · {c.active ? `owes a close each month from ${c.since}` : "inactive"}
              {c.last_period && ` · latest close ${c.last_period}`}
              {c.active && c.owed.length > 0 && <span className="status warn"> Not opened: {c.owed.join(", ")}</span>}
              {(c.due_days !== null || c.owner !== null) && (
                <small>
                  {" "}
                  · own terms: {c.due_days === null ? "the template's due date" : c.due_days === 0 ? "no due date" : `due ${c.due_days} days after month end`}, {c.owner === null ? "the template's owner" : c.owner ? name(c.owner) : "no owner"}
                </small>
              )}
              <small> · {c.reason} ({c.actor}, {new Date(c.at).toLocaleDateString()})</small>
              {roster.can_manage && <ClientForm key={`${c.entity}:${c.at}`} client={c} people={people.filter((p) => p.firm_wide || p.clients.some((e) => e.toLowerCase() === c.entity.toLowerCase()))} onSaved={() => setRefresh((n) => n + 1)} />}
            </li>
          ))}
        </ul>
      )}
      {roster.can_manage ? <ClientForm people={people.filter((p) => p.firm_wide)} onSaved={() => setRefresh((n) => n + 1)} /> : <p className="note">A firm administrator with clients:all and members:manage changes the roster and the template.</p>}
    </section>
  );
}

function TemplateForm({ template, people, onSaved }: { template: Template; people: Person[]; onSaved: () => void }) {
  const [due, setDue] = useState(template?.due_days ? String(template.due_days) : "");
  const [owner, setOwner] = useState(template?.owner ?? "");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function save() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/close-template", { due_days: due ? Number(due) : null, owner, reason: reason.trim() });
      setReason("");
      onSaved();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The template could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <details>
      <summary>Change the monthly template</summary>
      <label>
        Due
        <select value={due} onChange={(e) => setDue(e.target.value)}>
          <option value="">No due date</option>
          {days.map((n) => <option key={n} value={n}>{n} {n === 1 ? "day" : "days"} after month end</option>)}
        </select>
      </label>
      <label>
        Owner
        <select value={owner} onChange={(e) => setOwner(e.target.value)}>
          <option value="">Nobody</option>
          {people.map((p) => <option key={p.subject} value={p.subject}>{p.name}</option>)}
        </select>
      </label>
      <label>
        Reason for the change
        <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
      </label>
      <button type="button" disabled={busy || !reason.trim()} onClick={() => void save()}>Save template</button>
      {message && <p role="alert">{message}</p>}
    </details>
  );
}

// Adds a client (without one) or changes a client's entry: active or not, the first month it owes, and its own terms.
function ClientForm({ client, people, onSaved }: { client?: Client; people: Person[]; onSaved: () => void }) {
  const [entity, setEntity] = useState(client?.entity ?? "");
  const [active, setActive] = useState(client?.active ?? true);
  const [since, setSince] = useState(client?.since ?? endedMonth());
  const [due, setDue] = useState(client?.due_days != null ? String(client.due_days) : "template");
  const [owner, setOwner] = useState(client?.owner === null || client?.owner === undefined ? "template" : client.owner);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function save() {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/roster", {
        entity: entity.trim(),
        active,
        since,
        due_days: due === "template" ? null : Number(due),
        owner: owner === "template" ? null : owner,
        reason: reason.trim(),
      });
      setReason("");
      if (!client) setEntity("");
      onSaved();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The roster could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <details>
      <summary>{client ? "Change" : "Add a client"}</summary>
      {!client && (
        <label>
          Client
          <input value={entity} maxLength={200} onChange={(e) => setEntity(e.target.value)} />
        </label>
      )}
      <label>
        <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} /> Active: owes a close each month
      </label>
      <label>
        First month owed
        <input type="month" value={since} onChange={(e) => setSince(e.target.value)} />
      </label>
      <label>
        Due
        <select value={due} onChange={(e) => setDue(e.target.value)}>
          <option value="template">As the firm template</option>
          <option value="0">No due date</option>
          {days.map((n) => <option key={n} value={n}>{n} {n === 1 ? "day" : "days"} after month end</option>)}
        </select>
      </label>
      <label>
        Owner
        <select value={owner} onChange={(e) => setOwner(e.target.value)}>
          <option value="template">As the firm template</option>
          <option value="">Nobody</option>
          {people.map((p) => <option key={p.subject} value={p.subject}>{p.name}</option>)}
        </select>
      </label>
      <label>
        Reason
        <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
      </label>
      <button type="button" disabled={busy || !reason.trim() || !entity.trim() || !since} onClick={() => void save()}>
        {client ? "Save" : "Add client"}
      </button>
      {message && <p role="alert">{message}</p>}
    </details>
  );
}
