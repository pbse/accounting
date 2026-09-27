import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { Icon } from "../dashboard/Icon";
import { AgentAccess } from "./AgentAccess";
import { ClientAccess } from "./ClientAccess";
import { FirmOperations } from "./Enterprise";
import { workspaceRequest } from "./workspace";

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
  email: null | { sender: string; enabled: boolean; verified_at?: string; connected_by: string; connected_at: string };
  posting_grants: Grant[];
  activity: { at: string; actor: string; text: string }[];
  providers: { provider: string; receives: string; evidence: string; in_use: boolean }[];
  clients: string[];
};

const day = (at: string) => new Date(at).toLocaleDateString();
const settings = (closeId: string) => `/dashboard/closes/${closeId}?tab=settings`;

function SuspendFirm({ organizationId }: { organizationId: string }) {
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function suspend(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/organization/revoke", { organization_id: data.get("organization_id"), reason: data.get("reason"), confirmed: data.get("confirmed") === "on" });
      setMessage("The firm is suspended. Every member and agent is refused from the next request.");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The suspension was not confirmed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <details>
      <summary>Suspend the whole firm</summary>
      <p>For a suspected compromise: every member, agent and client link of this firm is refused until an operator restores it. Requires org:manage.</p>
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
          <label>
            <input name="confirmed" type="checkbox" required /> I am suspending every member's and agent's access to this firm.
          </label>
          <button>Suspend the firm</button>
        </fieldset>
      </form>
      {message && <p role="status">{message}</p>}
    </details>
  );
}

// One place for who can do what, which services and agents act for the firm, what they receive, and what changed.
export default function SecurityCenter() {
  const [security, setSecurity] = useState<Security>();
  const [error, setError] = useState("");
  useEffect(() => {
    workspaceRequest("/security")
      .then(setSecurity)
      .catch((e) => setError(e instanceof Error ? e.message : "The security center is unavailable."));
  }, []);
  return (
    <>
      <div className="page-title">
        <div>
          <span className="kpi-icon" aria-hidden="true">
            <Icon name="shield" />
          </span>
          <span>
            <h1>Security center</h1>
            <p>People, client assignments, agents, connected services, posting authority, what outside services receive, and recent changes. Firm administrators only.</p>
          </span>
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      {security && (
        <div className="stack">
          <section aria-label="People">
            <h2>People and their agents</h2>
            <p className="note">Firm-wide access comes from each person's WorkOS role; client assignments below apply only while their membership is enrolled.</p>
            <ul>
              {security.people.map((p) => (
                <li key={p.subject}>
                  <strong>{p.subject}</strong> · {p.granted ? "enrolled" : "revoked"} by {p.decided_by} on {day(p.decided_at)}: {p.reason}
                  <br />
                  Clients: {p.assignments.length ? p.assignments.join(", ") : "none assigned"}
                  {!p.granted && p.assignments.length > 0 && " (recorded, not in effect while revoked)"}
                  <br />
                  Agent in force: {p.agent_scope_in_force ?? "none"}
                  {p.agent && ` · latest decision: ${p.agent.state} for ${p.agent.scope} by ${p.agent.decided_by} on ${day(p.agent.decided_at)}`}
                </li>
              ))}
            </ul>
            {security.invitations.length > 0 && (
              <p>
                Pending invitations: {security.invitations.map((i) => `${i.email} as ${i.role} (by ${i.invited_by}${i.expires_at ? `, expires ${day(i.expires_at)}` : ""})`).join("; ")}
              </p>
            )}
          </section>
          <section aria-label="Connected services">
            <h2>Connected services</h2>
            {security.services.length === 0 ? (
              <p className="empty">No ledger or payout connections.</p>
            ) : (
              <ul>
                {security.services.map((s) => (
                  <li key={s.provider + s.close_id + s.connected_at}>
                    <strong>{s.provider}</strong> {s.mode && `(${s.mode})`} · {s.client}
                    {s.account && ` · ${s.account}`} · {s.state.replaceAll("_", " ")} · connected by {s.connected_by} on {day(s.connected_at)}
                    {s.confirmed_by && `, confirmed by ${s.confirmed_by}`} · <Link to={settings(s.close_id)}>Manage or disconnect</Link>
                  </li>
                ))}
              </ul>
            )}
            <p>
              Email sending: {security.email ? `${security.email.sender}, ${security.email.enabled ? "enabled" : "disabled"}${security.email.verified_at ? ", verified" : ", not verified"}, set by ${security.email.connected_by}` : "not set up"} ·{" "}
              <Link to="/dashboard/firm">Manage in Firm settings</Link>
            </p>
            <p className="note">Google Drive grants are held by WorkOS for each person, not stored here; manage them in WorkOS.</p>
          </section>
          <section aria-label="Posting authority">
            <h2>Posting authority in force</h2>
            <p className="note">Only grants posting would honour now: unrevoked, unexpired, granted by a current member, on an active connection to the granted company.</p>
            {security.posting_grants.length === 0 ? (
              <p className="empty">No grant allows writing to a client's live ledger.</p>
            ) : (
              <ul>
                {security.posting_grants.map((g) => (
                  <li key={g.provider + g.client + g.granted_at}>
                    <strong>{g.provider}</strong> · {g.client} · {g.kinds.join(", ")} to accounts {g.account_ids.join(", ")}, up to {g.max_amount}, until {day(g.expires_at)} · granted by {g.granted_by} on {day(g.granted_at)}: {g.reason}
                    {g.close_id && (
                      <>
                        {" "}
                        · <Link to={settings(g.close_id)}>Revoke</Link>
                      </>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </section>
          <section aria-label="What outside services receive">
            <h2>What outside services receive</h2>
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
          </section>
          <section aria-label="Recent changes">
            <h2>Recent changes</h2>
            <p className="note">The newest 100 recorded access decisions, connection and grant changes, and records agents made for members. Derived from those histories; not a complete audit trail.</p>
            <ol>
              {security.activity.map((e, i) => (
                <li key={i}>
                  {new Date(e.at).toLocaleString()} · {e.actor}: {e.text}
                </li>
              ))}
            </ol>
          </section>
          <h2>Controls</h2>
          <FirmOperations />
          <ClientAccess entities={security.clients} />
          <AgentAccess />
          <SuspendFirm organizationId={security.organization_id} />
        </div>
      )}
    </>
  );
}
