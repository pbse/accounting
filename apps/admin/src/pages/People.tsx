import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { post, useResource, when } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "../components/ActionForm";

type Session = { id: string; organization_id: string; status: string; auth_method: string; created_at: string; expires_at: string; ended_at: string | null };
type WorkOSUser = {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  last_sign_in_at: string | null;
  memberships: { organization_id: string; firm_name: string; status: string; role: { slug: string } }[];
  sessions: Session[];
};
type Membership = { subject: string; organization_id: string; firm_name: string; firm_enabled: boolean; id: string; granted: boolean; decided_by: string; decided_at: string; assignments: number; agent: string | null };
type Invitation = { id: string; organization_id: string; firm_name: string; email: string; role: string; state: string; invited_by: string; created_at: string; expires_at: string | null };
type Found = { query: string; workos: "ok" | "not_configured" | "unavailable"; from_app_records: boolean; users: WorkOSUser[]; memberships: Membership[]; invitations: Invitation[] };

const firmLink = (id: string, name: string, tab = "people") => (name ? <Link to={`/firms/${encodeURIComponent(id)}?tab=${tab}`}>{name}</Link> : id);

// One person across every firm: their WorkOS account, memberships and sessions, and the app's
// memberships and invitations for them.
export function People() {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const found = useResource<Found>(q ? `/v1/admin/people?q=${encodeURIComponent(q)}` : null);
  const operate = usePermissions().includes("platform:operate");
  const [said, setSaid] = useState("");
  return (
    <>
      <div className="page-title"><div><div><h1>People</h1><p>Find one person across every firm by their WorkOS user ID or email address.</p></div></div></div>
      <form className="filters" onSubmit={(event) => {
        event.preventDefault();
        setSaid("");
        const value = String(new FormData(event.currentTarget).get("q") ?? "").trim();
        setParams(value ? { q: value } : {});
      }}>
        <label>User ID or email<input name="q" key={q} defaultValue={q} placeholder="user_… or name@firm.com" required /></label>
        <button>Search</button>
      </form>
      {!q && <p>Search to see a person's firms, sessions and invitations.</p>}
      {q && found.error && <p role="alert">{found.error}</p>}
      {q && !found.data && !found.error && <p role="status">Searching…</p>}
      {said && <p className="note" role="status">{said}</p>}
      {q && found.data && (
        <>
          {found.data.workos === "not_configured" && <p className="note">The server has no WorkOS key, so only the app's records show.</p>}
          {found.data.workos === "unavailable" && <p role="alert">WorkOS did not answer, so only the app's records show. Try again shortly.</p>}
          {found.data.workos === "ok" && found.data.users.length === 0 && <p>WorkOS has no account for “{found.data.query}”.</p>}
          {found.data.users.map((u) => (
            <section key={u.id}>
              <h2>{[u.first_name, u.last_name].filter(Boolean).join(" ") || u.email}</h2>
              <p>{u.email} · {u.id} · last signed in {when(u.last_sign_in_at)}</p>
              <h3>WorkOS memberships</h3>
              {u.memberships.length === 0 ? <p>None.</p> : (
                <table>
                  <thead><tr><th>Organization</th><th>Role</th><th>Status</th></tr></thead>
                  <tbody>
                    {u.memberships.map((m) => (
                      <tr key={m.organization_id}>
                        <td>{m.firm_name && m.firm_name !== "Platform staff" ? firmLink(m.organization_id, m.firm_name) : m.firm_name || m.organization_id}</td>
                        <td>{m.role.slug}</td>
                        <td>{m.status}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <h3>Newest sessions</h3>
              {u.sessions.length === 0 ? <p>None.</p> : (
                <table>
                  <thead><tr><th>Organization</th><th>Status</th><th>Signed in with</th><th>Started</th><th>Expires</th><th>Ended</th></tr></thead>
                  <tbody>
                    {u.sessions.map((s) => (
                      <tr key={s.id}>
                        <td>{s.organization_id || "—"}</td>
                        <td>{s.status}</td>
                        <td>{s.auth_method || "—"}</td>
                        <td>{when(s.created_at)}</td>
                        <td>{when(s.expires_at)}</td>
                        <td>{when(s.ended_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              {/* Always offered: the action reads every session, not just the newest shown, and the app's own. */}
              <details>
                <summary>End all sessions</summary>
                <p>Signs {u.email} out everywhere. Sessions the app holds end at once. Every active WorkOS session, in every organization, is revoked, so none can be renewed; a browser that already holds an access token keeps it until it expires, within minutes. Their memberships stay as they are.</p>
                <ActionForm label="End all sessions" danger allowed={operate} minLength={10} onSubmit={async (reason) => {
                  const answer = await post<{ ended: number; app_sessions: number }>(`/v1/admin/people/${encodeURIComponent(u.id)}/sessions/revoke`, { reason });
                  setSaid(`Signed ${u.email} out: ${answer.ended} WorkOS session${answer.ended === 1 ? "" : "s"} and ${answer.app_sessions} app session${answer.app_sessions === 1 ? "" : "s"} ended.`);
                  found.reload();
                }} />
              </details>
            </section>
          ))}
          <section>
            <h2>Firm memberships in the app</h2>
            {found.data.from_app_records && <p className="note">Found through the app's own records of this address, which may include earlier owners of it. Check the member IDs.</p>}
            {found.data.memberships.length === 0 ? <p>No firm has a membership decision for this person.</p> : (
              <table>
                <thead><tr><th>Firm</th><th>Member</th><th>Access</th><th>Clients</th><th>Agent</th><th>Decided by</th><th>When</th></tr></thead>
                <tbody>
                  {found.data.memberships.map((m) => (
                    <tr key={`${m.organization_id}-${m.subject}`}>
                      <td>{firmLink(m.organization_id, m.firm_name)}{!m.firm_enabled && <small> suspended</small>}</td>
                      <td>{m.subject}</td>
                      <td><span className={`status ${m.granted ? "ok" : "danger"}`}>{m.granted ? "Granted" : "Revoked"}</span></td>
                      <td>{m.assignments}</td>
                      <td>{m.agent ?? "—"}</td>
                      <td>{m.decided_by}</td>
                      <td>{when(m.decided_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            <p className="muted">Revoke a membership from the firm's People tab, where the decision you are replacing shows.</p>
          </section>
          <section>
            <h2>Invitations</h2>
            {found.data.invitations.length === 0 ? <p>None.</p> : (
              <table>
                <thead><tr><th>Firm</th><th>Email</th><th>Role</th><th>State</th><th>Invited by</th><th>Sent</th><th>Expires</th><th /></tr></thead>
                <tbody>
                  {found.data.invitations.map((i) => (
                    <tr key={i.id}>
                      <td>{firmLink(i.organization_id, i.firm_name)}</td>
                      <td>{i.email}</td>
                      <td>{i.role}</td>
                      <td>{i.state}</td>
                      <td>{i.invited_by}</td>
                      <td>{when(i.created_at)}</td>
                      <td>{when(i.expires_at)}</td>
                      <td>
                        <InvitationActions firm={i.organization_id} invitation={i} allowed={operate} onDone={(message) => { setSaid(message); found.reload(); }} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </>
      )}
    </>
  );
}

// Resend asks WorkOS to email a pending invitation again; revoke closes an open one in the app,
// then in WorkOS. Each says what happened.
export function InvitationActions({ firm, invitation, allowed, onDone }: { firm: string; invitation: { id: string; state: string }; allowed: boolean; onDone: (message: string) => void }) {
  const base = `/v1/admin/firms/${encodeURIComponent(firm)}/invitations/${encodeURIComponent(invitation.id)}`;
  const open = invitation.state === "sending" || invitation.state === "pending";
  if (!open) return null;
  return (
    <>
      {invitation.state === "pending" && (
        <details>
          <summary>Resend</summary>
          <ActionForm label="Resend invitation" allowed={allowed} minLength={10} onSubmit={async (reason) => {
            const answer = await post<{ message: string }>(`${base}/resend`, { reason });
            onDone(answer.message);
          }} />
        </details>
      )}
      <details>
        <summary>Revoke</summary>
        <ActionForm label="Revoke invitation" danger allowed={allowed} minLength={10} onSubmit={async (reason) => {
          const answer = await post<{ message: string }>(`${base}/revoke`, { reason });
          onDone(answer.message);
        }} />
      </details>
    </>
  );
}
