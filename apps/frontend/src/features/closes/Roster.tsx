import { useEffect, useState } from "react";
import { forbidden } from "./requestError";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceRequest } from "./workspace";

// rev changes with every change, so a form keyed on it resets to the newest entry.
type Template = { due_days: number | null; owner: string; reason: string; actor: string; at: string; rev: string } | null;
type Client = { entity: string; active: boolean; since: string; due_days: number | null; owner: string | null; reason: string; actor: string; at: string; rev: string; last_period: string; owed: string[] };
// clients are the names (lowercase) a reviewer is assigned to, as access to a client's closes is granted.
type Person = { subject: string; name: string; firm_wide: boolean; clients: string[] };
type Roster = { template: Template; template_hidden?: boolean; clients: Client[]; can_manage: boolean; people?: Person[]; people_unavailable?: string };

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
  // A member reads only the clients assigned to them; a firm the server refuses hides the section.
  const [hidden, setHidden] = useState(false);
  // A save's refusal, kept while the roster is read again: a change made from an entry another administrator has since
  // changed resets to the newest entry, and this says why.
  const [notice, setNotice] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [editingTemplate, setEditingTemplate] = useState(false);
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState("");
  const saved = () => (setNotice(""), setRefresh((n) => n + 1));
  // A dialog left, with or without a refused save, reads the roster again, so the next attempt starts from the newest entry.
  // The dialog's own close follows Cancel, so an empty message keeps the refusal Cancel passed.
  const left = (message = "") => (setNotice((kept) => message || kept), setAdding(false), setEditing(""), setEditingTemplate(false), setRefresh((n) => n + 1));
  useEffect(() => {
    let live = true;
    workspaceRequest("/roster")
      .then((data: Roster) => live && (setRoster(data), setError("")))
      .catch((e) => live && (forbidden(e) ? setHidden(true) : setError(e instanceof Error ? e.message : "The client roster is unavailable.")));
    return () => {
      live = false;
    };
  }, [refresh]);
  if (hidden) return null;
  if (!roster) return error ? <section aria-label="Clients and monthly closes"><h2>Clients and monthly closes</h2><p role="alert">{error}</p></section> : null;
  const people = roster.people ?? [];
  const name = (subject: string) => people.find((p) => p.subject === subject)?.name ?? subject;
  const t = roster.template;
  return (
    <section aria-label="Clients and monthly closes">
      <SectionHeader title="Clients and monthly closes">
        {roster.can_manage && (
          <span className="setting-actions">
            <button type="button" onClick={() => setAdding(true)}>
              Add a client
            </button>
            <button type="button" onClick={() => setEditingTemplate(true)}>
              Monthly template
            </button>
          </span>
        )}
      </SectionHeader>
      <p>
        Every active client owes a close each month. A month nobody has opened shows in Work until it is opened, and opening it gives the close{" "}
        {t ? (
          <>
            {t.due_days ? `a due date ${t.due_days} ${t.due_days === 1 ? "day" : "days"} after the month ends` : "no due date"} and {t.owner ? <>{name(t.owner)} as its owner</> : "no owner"}
          </>
        ) : roster.template_hidden ? (
          "the due date and owner of the firm's monthly template"
        ) : (
          "no due date or owner until the firm sets its monthly template"
        )}
        , unless the client's own entry says otherwise.
      </p>
      {error && <p role="alert">{error} Showing the last roster read.</p>}
      {notice && <p role="alert">{notice}</p>}
      {roster.people_unavailable && <p role="alert">{roster.people_unavailable}</p>}
      {roster.clients.length === 0 ? (
        <p className="empty">No client is on the roster yet. A client joins when it is taken over, or when a firm administrator adds it.</p>
      ) : (
        <RowList>
          {roster.clients.map((c) => (
            <div className="row" key={c.entity}>
              <EntityCell
                name={c.entity}
                title={c.entity}
                detail={
                  <>
                    {c.active ? `Owes a close each month from ${c.since}` : "Inactive"}
                    {c.last_period && ` · latest close ${c.last_period}`}
                  </>
                }
              >
                <small>
                  {c.reason} ({name(c.actor)}, {new Date(c.at).toLocaleDateString()})
                </small>
              </EntityCell>
              <span>
                {c.active && c.owed.length > 0 ? <span className="pill muted">Not opened: {c.owed.join(", ")}</span> : c.active ? <span className="pill">Up to date</span> : <span className="pill neutral">Inactive</span>}
              </span>
              <small>
                {c.due_days === null ? "Template due date" : c.due_days === 0 ? "No due date" : `Due ${c.due_days} days after month end`} ·{" "}
                {c.owner === null ? "template owner" : c.owner ? name(c.owner) : "no owner"}
              </small>
              {roster.can_manage ? (
                <button type="button" onClick={() => setEditing(c.entity)}>
                  Change
                </button>
              ) : (
                <span />
              )}
            </div>
          ))}
        </RowList>
      )}
      {!roster.can_manage && <p className="note">A firm administrator changes the roster and the template.</p>}
      {roster.can_manage && (
        <>
          <Dialog open={editingTemplate} onClose={() => left()} title="The monthly template" description="The due date and owner every client's new month takes, unless the client's own entry says otherwise.">
            {t && <p className="note">Set by {name(t.actor)} on {new Date(t.at).toLocaleDateString()}: {t.reason}</p>}
            <TemplateForm key={t?.rev ?? "none"} template={t} people={people.filter((p) => p.firm_wide)} onSaved={() => (setEditingTemplate(false), saved())} onCancel={left} />
          </Dialog>
          <Dialog open={adding} onClose={() => left()} title="Add a client" description="A client on the roster owes a close each month from its first month.">
            <ClientForm key={roster.clients.length} people={people} onSaved={() => (setAdding(false), saved())} onCancel={left} />
          </Dialog>
          {roster.clients.map((c) => (
            <Dialog key={c.entity} open={editing === c.entity} onClose={() => left()} title={c.entity} description="Whether the client owes a close each month, from when, and its own due date and owner.">
              <ClientForm key={`${c.entity}:${c.rev}`} client={c} people={people} onSaved={() => (setEditing(""), saved())} onCancel={left} />
            </Dialog>
          ))}
        </>
      )}
    </section>
  );
}

// onCancel carries the last refusal, if any, so the roster can read itself again.
type Saved = { onSaved: () => void; onCancel: (failed: string) => void };

function TemplateForm({ template, people, onSaved, onCancel }: { template: Template; people: Person[] } & Saved) {
  const [due, setDue] = useState(template?.due_days ? String(template.due_days) : "");
  const [owner, setOwner] = useState(template?.owner ?? "");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // An owner who has since lost the membership or role to hold every client's closes is shown as such, and must be
  // replaced before the template is saved.
  const ineligible = owner !== "" && !people.some((p) => p.subject === owner);
  async function save() {
    setBusy(true);
    try {
      await workspaceRequest("/close-template", { due_days: due ? Number(due) : null, owner, reason: reason.trim(), rev: template?.rev ?? "" });
      setReason("");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The template could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={(e) => (e.preventDefault(), void save())}>
      <fieldset disabled={busy}>
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
          {ineligible && <option value={owner}>{owner}: can no longer hold these closes</option>}
          {people.map((p) => <option key={p.subject} value={p.subject}>{p.name}</option>)}
        </select>
      </label>
      {ineligible && <p className="note">The template's owner can no longer hold every client's closes. Choose another owner, or nobody, to save.</p>}
      <label>
        Reason for the change
        <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
      </label>
      {error && <p role="alert">{error}</p>}
      <DialogActions>
        <button type="button" onClick={() => onCancel(error)}>Cancel</button>
        <button className="primary" disabled={busy || !reason.trim() || ineligible}>Save template</button>
      </DialogActions>
      </fieldset>
    </form>
  );
}

// Adds a client (without one) or changes a client's entry: active or not, the first month it owes, and its own terms.
function ClientForm({ client, people, onSaved, onCancel }: { client?: Client; people: Person[] } & Saved) {
  const [entity, setEntity] = useState(client?.entity ?? "");
  const [active, setActive] = useState(client?.active ?? true);
  const [since, setSince] = useState(client?.since ?? endedMonth());
  const [due, setDue] = useState(client?.due_days != null ? String(client.due_days) : "template");
  const [owner, setOwner] = useState(client?.owner === null || client?.owner === undefined ? "template" : client.owner);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // Owners who can do this client's work: a role reviewing every client, or a reviewer assigned to it.
  const named = entity.trim().toLowerCase();
  const owners = people.filter((p) => p.firm_wide || (named !== "" && p.clients.includes(named)));
  // An owner who has since lost the membership, role or assignment to do this client's work is shown as such, and must
  // be replaced before the entry is saved.
  const ineligible = owner !== "template" && owner !== "" && !owners.some((p) => p.subject === owner);
  async function save() {
    setBusy(true);
    try {
      await workspaceRequest("/roster", {
        entity: entity.trim(),
        active,
        since,
        due_days: due === "template" ? null : Number(due),
        owner: owner === "template" ? null : owner,
        reason: reason.trim(),
        rev: client?.rev ?? "",
      });
      setReason("");
      if (!client) setEntity("");
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The roster could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={(e) => (e.preventDefault(), void save())}>
      <fieldset disabled={busy}>
      {!client && (
        <label>
          Client
          <input value={entity} maxLength={200} onChange={(e) => setEntity(e.target.value)} />
        </label>
      )}
      <label className="checkbox-label">
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
          {ineligible && <option value={owner}>{owner}: can no longer hold these closes</option>}
          {owners.map((p) => <option key={p.subject} value={p.subject}>{p.name}</option>)}
        </select>
      </label>
      {ineligible && <p className="note">This owner can no longer do {entity.trim() || "this client"}'s work. Choose another owner, nobody, or the template's to save.</p>}
      <label>
        Reason
        <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
      </label>
      {error && <p role="alert">{error}</p>}
      <DialogActions>
        <button type="button" onClick={() => onCancel(error)}>Cancel</button>
        <button className="primary" disabled={busy || !reason.trim() || !entity.trim() || !since || ineligible}>
          {client ? "Save" : "Add client"}
        </button>
      </DialogActions>
      </fieldset>
    </form>
  );
}
