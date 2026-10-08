import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router";
import { authenticationHeaders } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
import { workspaceRequest } from "./workspace";

type Member = { id: number; subject: string; granted: boolean; reason: string; actor: string; created_at: string };
type Alert = { kind: string; close_id: string; item_id: string; state: string; age_minutes: number };
type Operations = { limits: { closes: number; active_jobs: number; model_calls_today: number }; usage: { closes: number; active_jobs: number; model_calls_today: number }; unmapped_delivery_events: number; undelivered_alerts: number; alerts: Alert[]; has_more: boolean };
type Retention = { retain_until: string; legal_hold: boolean; version: number; deletion_blockers: string[] };
type Person = { subject: string; email: string; name: string; role: string; status: string; access: "granted" | "revoked" | "none" };
type RoleOption = { slug: string; name: string; description: string; permissions: string[]; allowed: boolean };
type RoleChange = { id: number; subject: string; from_role: string; to_role: string; reason: string; actor: string; state: "pending" | "applied" | "failed" | "unknown"; created_at: string };

// The firm's people and their roles (docs/sign-in.md). Colleagues who sign in with the firm's company domain arrive
// with the Member role, which opens nothing until an administrator gives them a role or assigns them clients.
export function FirmPeople() {
  const [page, setPage] = useState<{ people: Person[]; roles: RoleOption[]; changes: RoleChange[]; next: string }>();
  const [subject, setSubject] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  // The first page, or the next one after the people already shown.
  async function load(after = "") {
    setBusy(true);
    try {
      const next = await workspaceRequest(`/members/people${after ? `?after=${encodeURIComponent(after)}` : ""}`);
      setPage((shown) => (after && shown ? { ...next, people: [...shown.people, ...next.people] } : next));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The firm’s people are unavailable.");
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  async function change(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    setBusy(true);
    setMessage("");
    try {
      const current = page?.people.find((person) => person.subject === subject)?.role ?? "";
      const result = await workspaceRequest("/members/role", { subject, role: data.get("role"), current, reason: data.get("reason"), confirmed: data.get("confirmed") === "on" });
      setMessage(result.message);
      form.reset();
      setSubject("");
      await load();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The role could not be changed.");
    } finally {
      setBusy(false);
    }
  }
  const roleName = (slug: string) => page?.roles.find((role) => role.slug === slug)?.name ?? slug;
  const who = (sub: string) => {
    const person = page?.people.find((p) => p.subject === sub);
    return person ? person.name || person.email : sub;
  };
  if (!page)
    return (
      <section aria-label="People and roles">
        <h2>People and roles</h2>
        <p role={message ? "alert" : "status"}>{message || "Loading the firm’s people…"}</p>
      </section>
    );
  const chosen = page.people.find((person) => person.subject === subject);
  return (
    <section aria-label="People and roles">
      <h2>People and roles</h2>
      <p>Colleagues who sign in with the firm’s company domain join with the Member role, which opens nothing until you give them a role or assign them clients. A role change takes effect within five minutes.</p>
      <table>
        <thead><tr><th>Person</th><th>Role</th><th>Firm access</th></tr></thead>
        <tbody>
          {page.people.map((person) => (
            <tr key={person.subject}>
              <td>{person.name ? <>{person.name}<br /><small>{person.email}</small></> : person.email}</td>
              <td>{roleName(person.role)}{person.status !== "active" && <small> · {person.status}</small>}</td>
              <td>{person.access === "granted" ? "Enrolled" : person.access === "revoked" ? "Removed" : "Not enrolled"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {page.next && <p><button type="button" disabled={busy} onClick={() => void load(page.next)}>Show more people</button></p>}
      <details><summary>Change a person’s role</summary><form onSubmit={(event) => void change(event)}><fieldset disabled={busy}>
        <label>Person
          <select required value={subject} onChange={(event) => setSubject(event.target.value)}>
            <option value="">Choose a person</option>
            {page.people.filter((person) => person.status === "active").map((person) => <option key={person.subject} value={person.subject}>{person.name ? `${person.name} (${person.email})` : person.email}</option>)}
          </select>
        </label>
        {chosen && <p>Current role: {roleName(chosen.role)}</p>}
        <label>New role
          <select name="role" required defaultValue="">
            <option value="" disabled>Choose a role</option>
            {page.roles.filter((role) => role.allowed).map((role) => <option key={role.slug} value={role.slug}>{role.name}{role.permissions.length ? ` — ${role.permissions.join(", ")}` : " — no access"}</option>)}
          </select>
        </label>
        <label>Reason<textarea name="reason" required minLength={10} maxLength={2000} /></label>
        <label className="checkbox-label"><input name="confirmed" type="checkbox" required />I checked the person and the role.</label>
        <button>Change role</button>
      </fieldset></form></details>
      {page.changes.length > 0 && <details><summary>Role changes ({page.changes.length})</summary><ol>{page.changes.map((c) => <li key={c.id}>{who(c.subject)}: {roleName(c.from_role)} → {roleName(c.to_role)}{c.state === "pending" ? " (awaiting WorkOS)" : c.state === "failed" ? " (not made)" : c.state === "unknown" ? " (outcome unknown: changed in WorkOS meanwhile)" : ""} · {c.reason} · by {who(c.actor)} · {new Date(c.created_at).toLocaleString()}</li>)}</ol></details>}
      {message && <p role="status">{message}</p>}
    </section>
  );
}

export function FirmEnrollment({ onEnrolled }: { onEnrolled: () => void }) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function enroll(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const data = new FormData(event.currentTarget);
      const response = await fetch(`${apiBaseUrl}/v1/organizations/enroll`, {
        method: "POST",
        headers: { ...(await authenticationHeaders()), "Content-Type": "application/json" },
        body: JSON.stringify({ name: data.get("name"), reason: data.get("reason"), confirmed: data.get("confirmed") === "on" }),
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.message ?? "Enrollment unavailable.");
      setMessage("Firm enrolled. Opening your workspace…");
      onEnrolled();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Enrollment unavailable.");
    } finally {
      setBusy(false);
    }
  }
  return <details><summary>Enroll a new firm</summary>
    <p>The first administrator needs a WorkOS organization token with org:enroll, members:manage and clients:all. Other members are enrolled by that administrator.</p>
    <form onSubmit={(event) => void enroll(event)}><fieldset disabled={busy}>
      <label>Firm name<input name="name" required maxLength={200} /></label>
      <label>Enrollment authority and reason<textarea name="reason" required minLength={10} maxLength={2000} /></label>
      <label><input name="confirmed" type="checkbox" required />I verified this WorkOS organization and have authority to enroll the firm.</label>
      <button>Enroll firm</button>
    </fieldset></form><p role="status">{message}</p>
  </details>;
}

type Role = { slug: string; name: string; description: string; permissions: string[]; allowed: boolean };
type Invitation = { id: string; email: string; role_name: string; state: string; invited_by: string; subject?: string; expires_at?: string; created_at: string };
const invitationStates: Record<string, string> = { sending: "Not yet confirmed by WorkOS", pending: "Waiting for them to accept", accepted: "Joined", revoked: "Revoked", expired: "Expired" };

export function Invitations() {
  const [roles, setRoles] = useState<{ configured: boolean; roles: Role[] }>();
  const [list, setList] = useState<Invitation[]>([]);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("");
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function load() {
    try {
      const [available, sent] = await Promise.all([workspaceRequest("/members/roles"), workspaceRequest("/members/invitations")]);
      setRoles(available);
      setList(sent);
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Invitations are unavailable.");
    }
  }
  useEffect(() => void load(), []);
  async function act(body: Record<string, unknown>) {
    setBusy(true);
    setMessage("");
    try {
      const result = await workspaceRequest("/members/invitations", body);
      setMessage(result.message);
      if (body.action === "send") {
        setEmail("");
        setReason("");
        setConfirmed(false);
      }
      await load();
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "The invitation could not be sent.");
    } finally {
      setBusy(false);
    }
  }
  if (roles && !roles.configured) return <section aria-label="Invite by email"><h2>Invitations</h2><p className="note">Email invitations need the server's WorkOS API key; until then, enroll members by their WorkOS user ID below.</p></section>;
  const chosen = roles?.roles.find((r) => r.slug === role);
  const hidden = roles?.roles.filter((r) => !r.allowed).length ?? 0;
  return (
    <section aria-label="Invite by email">
      <h2>Invitations</h2>
      <p>WorkOS emails the invitation. Once they accept it and sign in, they join the firm. Assign their clients under client access if their role cannot see every client.</p>
      <details open={list.length === 0}><summary>Invite by email</summary><form onSubmit={(event) => { event.preventDefault(); void act({ action: "send", email, role, reason, confirmed }); }}>
        <fieldset disabled={busy}>
          <label>Email<input type="email" required maxLength={254} value={email} onChange={(event) => setEmail(event.target.value)} /></label>
          <label>Role<select required value={role} onChange={(event) => setRole(event.target.value)}><option value="">Choose a role</option>{roles?.roles.filter((r) => r.allowed).map((r) => <option key={r.slug} value={r.slug}>{r.name}</option>)}</select></label>
          {chosen && <>{chosen.description && <p>{chosen.description}</p>}<ul>{chosen.permissions.map((p) => <li key={p}>{p}</li>)}</ul></>}
          {hidden > 0 && <p>{hidden} role{hidden === 1 ? " grants" : "s grant"} permissions you don't hold; only an administrator who holds them can invite into {hidden === 1 ? "it" : "them"}.</p>}
          <label>Authority and reason<textarea required minLength={10} maxLength={2000} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
          <label className="checkbox-label"><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /> I verified the address and the role.</label>
          <button disabled={!confirmed || !role}>Send invitation</button>
        </fieldset>
      </form></details>
      {list.length > 0 && (
        <ul>
          {list.map((i) => (
            <li key={i.id}>
              {i.email} · {i.role_name} · {invitationStates[i.state] ?? i.state} · invited by {i.invited_by} {new Date(i.created_at).toLocaleDateString()}
              {(i.state === "pending" || i.state === "sending") && <> <button type="button" disabled={busy} onClick={() => void act({ action: "revoke", id: i.id, confirmed: true })}>Revoke</button></>}
            </li>
          ))}
        </ul>
      )}
      {message && <p role="status">{message}</p>}
    </section>
  );
}

// The firm's usage against its limits and any aged or unknown work, for an administrator checking on operations.
export function OperationsSnapshot() {
  const [snapshot, setSnapshot] = useState<Operations>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function load() {
    setBusy(true);
    setMessage("");
    try {
      setSnapshot(await workspaceRequest("/operations"));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Firm operations unavailable.");
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void load();
  }, []);
  if (!snapshot) return message ? <p role="alert">{message}</p> : <p role="status">Reading usage…</p>;
  return (
    <>
      <p>
        Closes {snapshot.usage.closes}/{snapshot.limits.closes} · active jobs {snapshot.usage.active_jobs}/{snapshot.limits.active_jobs} · model calls today (UTC) {snapshot.usage.model_calls_today}/{snapshot.limits.model_calls_today} · undelivered external alerts {snapshot.undelivered_alerts} · unmapped historical delivery events {snapshot.unmapped_delivery_events}
      </p>
      {snapshot.alerts.length === 0 ? <p className="note">No aged or unknown work.</p> : <ul>{snapshot.alerts.map((item) => <li key={`${item.kind}-${item.item_id}`}><Link to={`/dashboard/closes/${item.close_id}`}>{item.kind} #{item.item_id}</Link> · {item.state} · {item.age_minutes} minutes</li>)}</ul>}
      {snapshot.has_more && <p>More than 100 alerts exist. Resolve these, then refresh to see the rest.</p>}
      <p className="setting-actions"><button type="button" disabled={busy} onClick={() => void load()}>Refresh</button></p>
      {message && <p role="alert">{message}</p>}
    </>
  );
}

// Enrolling or revoking an exact WorkOS user, and the history of those decisions. WorkOS still owns identity and
// roles; client assignments remain separate.
export function MemberDecision() {
  const [members, setMembers] = useState<Member[]>();
  const [subject, setSubject] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function load() {
    try {
      setMembers(await workspaceRequest("/members"));
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Membership history unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, []);
  const current = members?.find((member) => member.subject === subject.trim());
  async function decide(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const data = new FormData(event.currentTarget);
      await workspaceRequest("/members", { subject: subject.trim(), granted: data.get("granted") === "on", previous_id: current ? String(current.id) : "", reason: data.get("reason"), confirmed: data.get("confirmed") === "on" });
      await load();
      setMessage("Firm membership decision saved. Revocation blocks subsequent API requests immediately; also revoke the WorkOS membership and session.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Membership decision unavailable.");
    } finally {
      setBusy(false);
    }
  }
  if (!members) return message ? <p role="alert">{message}</p> : null;
  return (
    <>
      <form onSubmit={(event) => void decide(event)}><fieldset disabled={busy}>
        <label>Exact WorkOS user ID<input required pattern="user_.+" maxLength={200} value={subject} onChange={(event) => setSubject(event.target.value)} /></label>
        <p>Current access: {current ? current.granted ? "Enrolled" : "Revoked" : "Not enrolled"}</p>
        <label className="checkbox-label"><input name="granted" type="checkbox" />Enroll this member (leave unchecked to revoke)</label>
        <label>Authority and reason<textarea name="reason" required minLength={10} maxLength={2000} /></label>
        <label className="checkbox-label"><input name="confirmed" type="checkbox" required />I verified the exact user and access decision.</label>
        <button>Save member decision</button>
      </fieldset></form>
      {members.length > 0 && <details><summary>Membership history ({members.length})</summary><ol>{members.map((member) => <li key={member.id}>{member.granted ? "Enrolled" : "Revoked"} · {member.subject} · {member.reason} · {member.actor} · {new Date(member.created_at).toLocaleString()}</li>)}</ol></details>}
      {message && <p role="status">{message}</p>}
    </>
  );
}

export function RetentionControls({ close, onDeleted }: { close: { id: string; entity: string; period: string }; onDeleted: (receipt: string) => void }) {
  const [status, setStatus] = useState<Retention>();
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function load() {
    setBusy(true);
    setMessage("");
    try { setStatus(await workspaceRequest(`/${close.id}/retention`)); }
    catch (error) { setMessage(error instanceof Error ? error.message : "Retention state unavailable."); }
    finally { setBusy(false); }
  }
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!status) return;
    setBusy(true);
    try {
      const data = new FormData(event.currentTarget);
      await workspaceRequest(`/${close.id}/retention`, { retain_until: data.get("retain_until"), legal_hold: data.get("legal_hold") === "on", expected_version: status.version, reason: data.get("reason"), confirmed: data.get("confirmed") === "on" });
      await load();
      setMessage("Retention decision saved. Review the updated deletion blockers before any removal.");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Retention decision unavailable."); }
    finally { setBusy(false); }
  }
  async function remove(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!status) return;
    setBusy(true);
    try {
      const data = new FormData(event.currentTarget);
      if (data.get("entity") !== close.entity || data.get("period") !== close.period) throw new Error("Enter the exact client and month shown above.");
      const result = await workspaceRequest(`/${close.id}/delete`, { expected_version: status.version, entity: close.entity, period: close.period, reason_code: data.get("reason_code"), confirmed: data.get("confirmed") === "on" });
      onDeleted(result.deletion_receipt_id);
    } catch (error) { setMessage(error instanceof Error ? error.message : "Deletion was not confirmed. Refresh before retrying."); }
    finally { setBusy(false); }
  }
  return <details onToggle={(event) => { if (event.currentTarget.open && !status && !busy) void load(); }}>
    <summary>Retention and deletion</summary>
    <p>Only a firm data administrator can set the approved date or legal hold. No date means no deletion. Backups follow the provider’s separately agreed expiry.</p>
    <button type="button" disabled={busy} onClick={() => void load()}>Refresh deletion preview</button>
    {status && <><p>Retain until: {status.retain_until || "not set"} · legal hold: {status.legal_hold ? "on" : "off"}</p>
      <form key={status.version} onSubmit={(event) => void save(event)}><fieldset disabled={busy}>
        <legend>Record the firm’s retention decision</legend>
        <label>Retain until<input name="retain_until" type="date" defaultValue={status.retain_until} /></label>
        <label><input name="legal_hold" type="checkbox" defaultChecked={status.legal_hold} />Legal hold prevents deletion</label>
        <label>Policy or authorization reference<textarea name="reason" required minLength={10} maxLength={2000} /></label>
        <label><input name="confirmed" type="checkbox" required />I verified this retention decision with the firm.</label>
        <button>Save retention decision</button>
      </fieldset></form>
      <h3>Deletion preview</h3>
      {status.deletion_blockers.length ? <ul>{status.deletion_blockers.map((blocker) => <li key={blocker}>{blocker}</li>)}</ul> : <p>The close is eligible for an explicit deletion decision. Download the binder or review package first if required by policy.</p>}
      {!status.deletion_blockers.length && <form onSubmit={(event) => void remove(event)}><fieldset disabled={busy}>
        <legend>Delete this close and its derived data</legend>
        <p>This removes saved work, evidence, job context, request history and posting receipts from the active database. It cannot recall exports or provider records.</p>
        <label>Type client name<input name="entity" required autoComplete="off" /></label>
        <label>Type close month<input name="period" required placeholder="YYYY-MM" autoComplete="off" /></label>
        <label>Approved reason<select name="reason_code"><option value="retention_expired">Retention period ended</option><option value="client_request">Client request</option><option value="contract_termination">Contract termination</option></select></label>
        <label><input name="confirmed" type="checkbox" required />I reviewed the exact scope and authorize permanent removal from the active database.</label>
        <button>Delete this close</button>
      </fieldset></form>}
    </>}
    <p role="status">{message}</p>
  </details>;
}
