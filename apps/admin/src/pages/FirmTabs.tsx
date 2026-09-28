import { useEffect, useState } from "react";
import { post, useResource, when } from "../api";
import { usePermissions } from "../App";
import { ActionForm } from "../components/ActionForm";

// What the server sends in place of client text until it is revealed.
const masked = "••••";
const shown = (value: string) => (value === masked ? <span className="muted">hidden</span> : value);

// useRevealable loads path, masked until someone reveals it with a reason. A reveal the server
// refuses is dropped, with its error, so the masked view and the form come back.
function useRevealable<T extends { revealed?: boolean }>(path: string) {
  const [reason, setReason] = useState<string>();
  const [failure, setFailure] = useState("");
  const resource = useResource<T>(path, reason);
  useEffect(() => {
    if (reason !== undefined && resource.error) {
      setFailure(resource.error);
      setReason(undefined);
    }
  }, [reason, resource.error]);
  return {
    ...resource,
    reveal: { pending: reason !== undefined && !resource.data?.revealed, shown: !!resource.data?.revealed, failure, show: (r: string) => { setFailure(""); setReason(r); }, hide: () => setReason(undefined) },
  };
}

// Reveal asks why before showing client content; each reveal is recorded in the audit log.
function Reveal({ reveal }: { reveal: ReturnType<typeof useRevealable>["reveal"] }) {
  const allowed = usePermissions().includes("platform:content");
  if (reveal.shown) return <p className="note">Showing client content. This view was recorded in the audit log. <button onClick={reveal.hide}>Hide it</button></p>;
  if (reveal.pending) return <p role="status">Revealing…</p>;
  return (
    <>
      {reveal.failure && <p role="alert">The content was not revealed: {reveal.failure}</p>}
      <details>
        <summary>Reveal client content</summary>
        <ActionForm label="Reveal client content" allowed={allowed} onSubmit={async (reason) => reveal.show(reason)} />
      </details>
    </>
  );
}

function Pager({ offset, more, next, onPage }: { offset: number; more: boolean; next: number; onPage: (offset: number) => void }) {
  if (!offset && !more) return null;
  return (
    <p className="title-actions">
      {offset > 0 && <button onClick={() => onPage(0)}>Newest</button>}
      {more && <button onClick={() => onPage(next)}>Older</button>}
    </p>
  );
}

type Person = {
  id: string;
  subject: string;
  granted: boolean;
  decided_by: string;
  decided_at: string;
  reason: string;
  assignments: string[];
  agent: { state: string; scope: string } | null;
  agent_scope_in_force: string | null;
};
type Invitation = { id: string; email: string; role: string; state: string; invited_by: string; created_at: string; expires_at: string | null; decided_at: string | null };

export function FirmPeople({ firm, onChanged }: { firm: string; onChanged: () => void }) {
  const people = useRevealable<{ people: Person[]; invitations: Invitation[]; revealed: boolean }>(`/v1/admin/firms/${encodeURIComponent(firm)}/people`);
  const operate = usePermissions().includes("platform:operate");
  const base = `/v1/admin/firms/${encodeURIComponent(firm)}`;
  return (
    <>
      <Reveal reveal={people.reveal} />
      {people.error && <p role="alert">{people.error}</p>}
      {!people.data && !people.error && <p role="status">Loading people…</p>}
      {people.data && (
        <>
          <section>
            <h2>Members</h2>
            <p>The app's membership decisions. Revoking one ends the person's access to this firm in the app; their WorkOS account is untouched.</p>
            {people.data.people.length === 0 ? <p>No member has been enrolled.</p> : (
              <table>
                <thead><tr><th>Member</th><th>Access</th><th>Clients</th><th>Agent</th><th>Decided by</th><th>Reason</th><th>When</th><th /></tr></thead>
                <tbody>
                  {people.data.people.map((p) => (
                    <tr key={p.subject}>
                      <td>{p.subject}</td>
                      <td><span className={`status ${p.granted ? "ok" : "danger"}`}>{p.granted ? "Granted" : "Revoked"}</span></td>
                      <td>{p.assignments.length}{p.assignments.some((a) => a !== masked) && <small> {p.assignments.join(", ")}</small>}</td>
                      <td>{p.agent_scope_in_force ?? p.agent?.state ?? "—"}</td>
                      <td>{p.decided_by}</td>
                      <td>{shown(p.reason)}</td>
                      <td>{when(p.decided_at)}</td>
                      <td>
                        {p.granted && (
                          <details>
                            <summary>Revoke</summary>
                            <ActionForm label="Revoke membership" danger allowed={operate} minLength={10} onSubmit={async (reason) => {
                              await post(`${base}/members/${encodeURIComponent(p.subject)}/revoke`, { previous_id: p.id, reason });
                              people.reload();
                              onChanged();
                            }} />
                          </details>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
          <section>
            <h2>Invitations</h2>
            {people.data.invitations.length === 0 ? <p>No invitation has been sent.</p> : (
              <table>
                <thead><tr><th>Email</th><th>Role</th><th>State</th><th>Invited by</th><th>Sent</th><th>Expires</th><th /></tr></thead>
                <tbody>
                  {people.data.invitations.map((i) => (
                    <tr key={i.id}>
                      <td>{i.email}</td>
                      <td>{i.role}</td>
                      <td>{i.state}</td>
                      <td>{i.invited_by}</td>
                      <td>{when(i.created_at)}</td>
                      <td>{when(i.expires_at)}</td>
                      <td>
                        {(i.state === "sending" || i.state === "pending") && (
                          <details>
                            <summary>Revoke</summary>
                            <ActionForm label="Revoke invitation" danger allowed={operate} minLength={10} onSubmit={async (reason) => {
                              await post(`${base}/invitations/${encodeURIComponent(i.id)}/revoke`, { reason });
                              people.reload();
                            }} />
                          </details>
                        )}
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

type Event = { at: string; actor: string; kind: string; text: string; masked: boolean };
const kinds = ["", "member", "client", "invitation", "agent_access", "agent.", "posting", "quickbooks", "xero", "stripe", "email", "workflow", "firm"];

export function FirmActivity({ firm }: { firm: string }) {
  const [kind, setKind] = useState("");
  const [offset, setOffset] = useState(0);
  const query = new URLSearchParams();
  if (kind) query.set("kind", kind);
  if (offset) query.set("offset", String(offset));
  const activity = useRevealable<{ events: Event[]; has_more: boolean; next_offset: number; revealed: boolean }>(`/v1/admin/firms/${encodeURIComponent(firm)}/activity${query.size ? `?${query}` : ""}`);
  return (
    <>
      <p>The firm's recorded access decisions, connections, posting grants, work agents did, and workflow runs, newest first.</p>
      <Reveal reveal={activity.reveal} />
      <div className="filters">
        <label>Kind
          <select value={kind} onChange={(event) => { setKind(event.target.value); setOffset(0); }}>
            {kinds.map((k) => <option key={k} value={k}>{k ? k.replace(/[._]$/, "").replaceAll("_", " ") : "Everything"}</option>)}
          </select>
        </label>
      </div>
      {activity.error && <p role="alert">{activity.error}</p>}
      {!activity.data && !activity.error && <p role="status">Loading activity…</p>}
      {activity.data && (
        <section>
          {activity.data.events.length === 0 ? <p>Nothing recorded.</p> : (
            <table>
              <thead><tr><th>When</th><th>Kind</th><th>By</th><th>What</th></tr></thead>
              <tbody>
                {activity.data.events.map((e, i) => (
                  <tr key={`${e.at}-${e.kind}-${i}`}>
                    <td>{when(e.at)}</td>
                    <td>{e.kind}</td>
                    <td>{e.actor || "—"}</td>
                    <td>{shown(e.text)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <Pager offset={offset} more={activity.data.has_more} next={activity.data.next_offset} onPage={setOffset} />
        </section>
      )}
    </>
  );
}

type Entry = { id: number; staff_user: string; action: string; target_type: string; target_id: string; reason: string; result: string; error: string; created_at: string };

export function FirmAudit({ firm }: { firm: string }) {
  const [offset, setOffset] = useState(0);
  const audit = useRevealable<{ entries: Entry[]; has_more: boolean; next_offset: number; revealed: boolean }>(`/v1/admin/firms/${encodeURIComponent(firm)}/audit${offset ? `?offset=${offset}` : ""}`);
  return (
    <>
      <p>Everything staff did to this firm, including actions that were refused and every reveal of client content. Reasons and why a refused action failed are hidden until revealed.</p>
      <Reveal reveal={audit.reveal} />
      {audit.error && <p role="alert">{audit.error}</p>}
      {!audit.data && !audit.error && <p role="status">Loading the audit…</p>}
      {audit.data && (
        <section>
          {audit.data.entries.length === 0 ? <p>Staff have not acted on this firm.</p> : (
            <table>
              <thead><tr><th>When</th><th>Staff</th><th>Action</th><th>Target</th><th>Reason</th><th>Result</th></tr></thead>
              <tbody>
                {audit.data.entries.map((e) => (
                  <tr key={e.id}>
                    <td>{when(e.created_at)}</td>
                    <td>{e.staff_user}</td>
                    <td>{e.action}</td>
                    <td>{e.target_type} {e.target_id}</td>
                    <td>{shown(e.reason)}</td>
                    <td><span className={`status ${e.result === "ok" ? "ok" : "danger"}`}>{e.result}</span>{e.error && <small> {shown(e.error)}</small>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <Pager offset={offset} more={audit.data.has_more} next={audit.data.next_offset} onPage={setOffset} />
        </section>
      )}
    </>
  );
}
