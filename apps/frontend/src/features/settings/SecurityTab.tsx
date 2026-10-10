import { useDates } from "../../data/time";
import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { RowList } from "@close/ui/components/RowList";
import { OperationsSnapshot } from "../closes/Enterprise";
import { workspaceRequest } from "../closes/workspace";

type Person = {
  subject: string;
  granted: boolean;
  decided_by: string;
  decided_at: string;
  reason: string;
  assignments: string[];
  agent: null | { state: string; scope: string; decided_by: string; decided_at: string };
  agent_scope_in_force: string | null;
};
type Service = { provider: string; close_id: string; client: string; account: string; mode: string; state: string; connected_by: string; connected_at: string; confirmed_by?: string; confirmed_at?: string };
type Grant = { provider: string; client: string; close_id?: string; company: string; account_ids: string[]; kinds: string[]; max_amount: string; expires_at: string; reason: string; granted_by: string; granted_at: string };
type Security = {
  organization_id: string;
  people: Person[];
  invitations: { email: string; role: string; invited_by: string; expires_at?: string; created_at: string }[];
  services: Service[];
  email: null | { provider?: string; sender: string; enabled: boolean; verified_at?: string; connected_by: string; connected_at: string };
  posting_grants: Grant[];
  activity: { at: string; actor: string; text: string }[];
  providers: { provider: string; receives: string; evidence: string; in_use: boolean }[];
  clients: string[];
  operations: Operation[];
};
type Recorded = { accepted: boolean; evidence: string; by: string; at: string } | null;
type Operation = {
  provider: string;
  kind: string;
  operation: string;
  status: string;
  clients: string[];
  sandbox_verified_at: string | null;
  live_verified_at: string | null;
  sandbox_close_id: number | null;
  live_close_id: number | null;
  sandbox_recorded: boolean;
  sandbox_checked: Recorded;
  accountant_accepted: Recorded;
  permission: string;
  owner: string;
  fallback: string;
  limitation: string;
};

const settings = (closeId: string) => `/dashboard/closes/${closeId}?tab=settings`;

function OperationRecord({ operation, stage, onSaved }: { operation: Operation; stage: "sandbox_checked" | "accountant_accepted"; onSaved: () => void }) {
  const day = useDates().date;
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState("");
  const current = operation[stage];
  const label = stage === "sandbox_checked" ? "Sandbox check outside the app" : "Accountant acceptance";
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    const accepted = form.get("accepted") === "yes";
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest("/security/operations", { provider: operation.provider, kind: operation.kind, stage, accepted, evidence: form.get("evidence") });
      element.reset();
      element.closest("details")?.removeAttribute("open");
      setSaved(`${label} ${accepted ? "recorded" : "withdrawn"} for ${operation.provider} ${operation.operation}.`);
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The record could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      {label}:{" "}
      {current ? (
        <>
          {current.accepted ? "recorded" : "withdrawn"} by {current.by} on {day(current.at)} <small>({current.evidence})</small>
        </>
      ) : (
        "none recorded"
      )}
      {saved && <p role="status">{saved}</p>}
      <details>
        <summary aria-label={`Record ${label.toLowerCase()} for ${operation.provider} ${operation.operation}`}>Record</summary>
        <form onSubmit={(e) => void save(e)}>
          <fieldset disabled={busy}>
            <label>
              Decision
              <select name="accepted" defaultValue="yes">
                <option value="yes">{stage === "sandbox_checked" ? "Checked and passed" : "Accept for this firm's use"}</option>
                <option value="no">Withdraw</option>
              </select>
            </label>
            <label>
              Evidence: what was checked, where, and its result or reference
              <textarea name="evidence" required minLength={5} maxLength={2000} />
            </label>
            {error && <p role="alert">{error}</p>}
            <button>Record</button>
          </fieldset>
        </form>
      </details>
    </div>
  );
}

function SuspendFirm({ organizationId }: { organizationId: string }) {
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function suspend(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      await workspaceRequest("/organization/revoke", { organization_id: data.get("organization_id"), reason: data.get("reason"), confirmed: data.get("confirmed") === "on" });
      setMessage("The firm is suspended. Every member and agent is refused from the next request.");
      setOpen(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The suspension was not confirmed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p>For a suspected compromise: every member, agent and client link of this firm is refused until an operator restores it. Requires org:manage.</p>
      <p className="setting-actions">
        <button type="button" onClick={() => (setError(""), setOpen(true))}>
          Suspend the firm…
        </button>
      </p>
      {message && <p role="status">{message}</p>}
      <Dialog open={open} onClose={() => setOpen(false)} title="Suspend the whole firm" description="Every member, agent and client link of this firm is refused from the next request until an operator restores it.">
        <form onSubmit={(e) => void suspend(e)}>
          <fieldset disabled={busy}>
            <label>
              Type this firm's ID ({organizationId})
              <input name="organization_id" required autoComplete="off" />
            </label>
            <label>
              Reason
              <textarea name="reason" required minLength={10} maxLength={2000} />
            </label>
            <label className="checkbox-label">
              <input name="confirmed" type="checkbox" required /> I am suspending every member's and agent's access to this firm.
            </label>
            {error && <p role="alert">{error}</p>}
            <DialogActions>
              <button type="button" onClick={() => setOpen(false)}>
                Cancel
              </button>
              <button className="primary">Suspend the firm</button>
            </DialogActions>
          </fieldset>
        </form>
      </Dialog>
    </>
  );
}

const state = (s: string) => s.replaceAll("_", " ");

// One place for which services and agents act for the firm, what they may write, what they receive, and what
// changed; the operator's tables and the controls that stop things sit folded below.
export function SecurityTab() {
  const dates = useDates();
  const day = dates.date;
  const read = useQuery(workspaceQuery<Security>("/security"));
  const security = read.data;
  const error = read.error ? read.error.message || "Security is unavailable." : "";
  // The usage snapshot aggregates across the firm's work, so it is read only once someone opens it.
  const [usage, setUsage] = useState(false);
  const refresh = () => void read.refetch();
  // A failed read shows with a way to try again; a view already read stays in place under it.
  const failed = error && (
    <section aria-label="Security unavailable">
      {!security && <h2>Security</h2>}
      <p role="alert">{error}</p>
      <p className="setting-actions">
        <button type="button" onClick={refresh}>
          Try again
        </button>
      </p>
    </section>
  );
  if (!security) return failed || <p role="status">Opening…</p>;
  return (
    <>
      {failed}
      <section aria-label="Connected services">
        <h2>Connected services</h2>
        <p>Each client's books and payouts, and who connected them. A connection is managed from the client's close.</p>
        {security.services.length === 0 ? (
          <p className="empty">No ledger or payout connections yet.</p>
        ) : (
          <RowList>
            {security.services.map((s) => (
              <div className="row" key={s.provider + s.close_id + s.connected_at}>
                <span>
                  <strong>
                    {s.provider}
                    {s.mode && ` (${s.mode})`} · {s.client}
                  </strong>
                  <small>
                    {s.account && `${s.account} · `}connected by {s.connected_by} on {day(s.connected_at)}
                    {s.confirmed_by && `, confirmed by ${s.confirmed_by}`}
                  </small>
                </span>
                <span className={`pill ${s.state === "connected" || s.state === "active" ? "" : "muted"}`}>{state(s.state)}</span>
                <span />
                <Link className="button" to={settings(s.close_id)}>
                  Manage
                </Link>
              </div>
            ))}
          </RowList>
        )}
        <p>
          Email sending:{" "}
          {security.email
            ? security.email.provider === "krith"
              ? `Krith's sender, ${security.email.sender}${security.email.enabled ? "" : ", disabled"}`
              : `${security.email.sender}, ${security.email.enabled ? "enabled" : "disabled"}${security.email.verified_at ? ", verified" : ", not verified"}, set by ${security.email.connected_by}`
            : "not set up"}{" "}
          ·{" "}
          <Link to="/dashboard/settings?tab=firm">Manage under Firm</Link>
        </p>
        <p className="note">Google Drive grants are held by the sign-in provider for each person, not stored here.</p>
      </section>
      <section aria-label="Posting authority">
        <h2>Posting authority in force</h2>
        <p className="note">Only grants posting would honour now: unrevoked, unexpired, granted by a current member, on an active connection to the granted company.</p>
        {security.posting_grants.length === 0 ? (
          <p className="empty">No grant allows writing to a client's live ledger.</p>
        ) : (
          <RowList>
            {security.posting_grants.map((g) => (
              <div className="row" key={g.provider + g.client + g.granted_at}>
                <span>
                  <strong>
                    {g.provider} · {g.client}
                  </strong>
                  <small>
                    {g.kinds.join(", ")} to accounts {g.account_ids.join(", ")}, up to {g.max_amount}, until {day(g.expires_at)}
                  </small>
                  <small>
                    Granted by {g.granted_by} on {day(g.granted_at)}: {g.reason}
                  </small>
                </span>
                <span />
                <span />
                {g.close_id ? (
                  <Link className="button" to={settings(g.close_id)}>
                    Revoke
                  </Link>
                ) : (
                  <span />
                )}
              </div>
            ))}
          </RowList>
        )}
      </section>
      <section aria-label="People">
        <h2>People and their agents</h2>
        <p className="note">Firm-wide access comes from each person's role; client assignments apply only while their membership is enrolled.</p>
        <RowList>
          {security.people.map((p) => (
            <div className="row" key={p.subject}>
              <span>
                <strong>{p.subject}</strong>
                <small>
                  {p.granted ? "Enrolled" : "Revoked"} by {p.decided_by} on {day(p.decided_at)}: {p.reason}
                </small>
                <small>
                  Clients: {p.assignments.length ? p.assignments.join(", ") : "none assigned"}
                  {!p.granted && p.assignments.length > 0 && " (recorded, not in effect while revoked)"}
                </small>
              </span>
              <span className={`pill ${p.granted ? "" : "danger"}`}>{p.granted ? "Enrolled" : "Revoked"}</span>
              <small>
                Agent: {p.agent_scope_in_force ?? "none"}
                {p.agent && ` · last ${p.agent.state} for ${p.agent.scope} by ${p.agent.decided_by} on ${day(p.agent.decided_at)}`}
              </small>
              <span />
            </div>
          ))}
        </RowList>
        {security.invitations.length > 0 && (
          <p>Pending invitations: {security.invitations.map((i) => `${i.email} as ${i.role} (by ${i.invited_by}${i.expires_at ? `, expires ${day(i.expires_at)}` : ""})`).join("; ")}</p>
        )}
      </section>
      <section aria-label="Recent changes">
        <h2>Recent changes</h2>
        <p className="note">The newest 100 recorded access decisions, connection and grant changes, and records agents made for members. Derived from those histories; not a complete audit trail.</p>
        {security.activity.length === 0 ? (
          <p className="empty">Nothing recorded yet.</p>
        ) : (
          <ol>
            {security.activity.map((e, i) => (
              <li key={i}>
                {dates.dateTime(e.at)} · {e.actor}: {e.text}
              </li>
            ))}
          </ol>
        )}
      </section>
      <section aria-label="Advanced">
        <h2>Advanced</h2>
        <p>What the operator proved and enabled, what outside services receive, the firm's usage, and the control that stops everything.</p>
        <details>
          <summary>Connector operations</summary>
          <p className="note">
            How far each QuickBooks, Xero and Stripe operation is proven and enabled here: documented, sandbox tested, production approved by this server, or enabled for the
            clients whose grant or connection covers it. Connected never means every operation is available. Checks done outside the app and the firm's accountant acceptance
            are dated records with their evidence; they never enable an operation, and an operation enabled without acceptance says so.
          </p>
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Operation</th>
                  <th>Status</th>
                  <th>Last verified</th>
                  <th>Needs</th>
                  <th>Limitation</th>
                  <th>Outside checks and acceptance</th>
                </tr>
              </thead>
              <tbody>
                {security.operations.map((o) => (
                  <tr key={o.provider + o.operation}>
                    <td>
                      {o.provider} · {o.operation}
                    </td>
                    <td>{o.status === "enabled" ? `Enabled for ${o.clients.join(", ")}` : o.status[0].toUpperCase() + o.status.slice(1)}</td>
                    <td>
                      Sandbox:{" "}
                      {o.sandbox_verified_at ? (
                        <Link to={`/dashboard/closes/${o.sandbox_close_id}`}>{day(o.sandbox_verified_at)}</Link>
                      ) : o.sandbox_recorded ? (
                        "none yet"
                      ) : (
                        "not recorded in the app"
                      )}
                      <br />
                      Live: {o.live_verified_at ? <Link to={`/dashboard/closes/${o.live_close_id}`}>{day(o.live_verified_at)}</Link> : "none yet"}
                    </td>
                    <td>
                      {o.permission}; approved by {o.owner}
                    </td>
                    <td>
                      {o.limitation} Otherwise: {o.fallback}.
                    </td>
                    <td>
                      {o.status === "enabled" && !o.accountant_accepted?.accepted && <p role="status">Enabled without the accountant's acceptance.</p>}
                      <OperationRecord operation={o} stage="sandbox_checked" onSaved={refresh} />
                      <OperationRecord operation={o} stage="accountant_accepted" onSaved={refresh} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
        <details>
          <summary>What outside services receive</summary>
          <table>
            <thead>
              <tr>
                <th>Service</th>
                <th>Receives</th>
                <th>In use here</th>
              </tr>
            </thead>
            <tbody>
              {security.providers.map((p) => (
                <tr key={p.provider}>
                  <td>{p.provider}</td>
                  <td>{p.receives}</td>
                  <td>{p.evidence}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
        <details onToggle={(e) => e.currentTarget.open && setUsage(true)}>
          <summary>Usage and alerts</summary>
          {usage && <OperationsSnapshot />}
        </details>
        <details>
          <summary>Suspend the whole firm</summary>
          <SuspendFirm organizationId={security.organization_id} />
        </details>
      </section>
    </>
  );
}
