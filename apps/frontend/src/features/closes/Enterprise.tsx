import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { authenticationHeaders } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
import { workspaceRequest } from "./workspace";

type Member = { id: number; subject: string; granted: boolean; reason: string; actor: string; created_at: string };
type Alert = { kind: string; close_id: string; item_id: string; state: string; age_minutes: number };
type Operations = { limits: { closes: number; active_jobs: number }; usage: { closes: number; active_jobs: number }; unmapped_delivery_events: number; undelivered_alerts: number; alerts: Alert[]; has_more: boolean };
type Retention = { retain_until: string; legal_hold: boolean; version: number; deletion_blockers: string[] };

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

export function FirmOperations() {
  const [snapshot, setSnapshot] = useState<Operations>();
  const [members, setMembers] = useState<Member[]>([]);
  const [memberAccess, setMemberAccess] = useState(false);
  const [subject, setSubject] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function load() {
    setBusy(true);
    setMessage("");
    try {
      const [ops, history] = await Promise.allSettled([workspaceRequest("/operations"), workspaceRequest("/members")]);
      if (ops.status === "fulfilled") setSnapshot(ops.value);
      if (history.status === "fulfilled") { setMembers(history.value); setMemberAccess(true); }
      if (ops.status === "rejected" && history.status === "rejected") throw ops.reason;
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Firm operations unavailable.");
    } finally {
      setBusy(false);
    }
  }
  const current = members.find((member) => member.subject === subject.trim());
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
  return <details onToggle={(event) => { if (event.currentTarget.open && !snapshot && !busy) void load(); }}>
    <summary>Firm operations and membership</summary>
    <p>Firm administrators can inspect durable exceptions and enroll or revoke exact WorkOS users. WorkOS still owns identity and roles; client assignments remain separate.</p>
    <button type="button" disabled={busy} onClick={() => void load()}>Refresh operations</button>
    {snapshot && <>
      <p role="status">Closes {snapshot.usage.closes}/{snapshot.limits.closes} · active jobs {snapshot.usage.active_jobs}/{snapshot.limits.active_jobs} · undelivered external alerts {snapshot.undelivered_alerts} · unmapped historical delivery events {snapshot.unmapped_delivery_events}</p>
      {snapshot.alerts.length === 0 ? <p>No aged or unknown work in this snapshot.</p> : <ul>{snapshot.alerts.map((item) => <li key={`${item.kind}-${item.item_id}`}><Link to={`/dashboard/closes/${item.close_id}`}>{item.kind} #{item.item_id}</Link> · {item.state} · {item.age_minutes} minutes</li>)}</ul>}
      {snapshot.has_more && <p>More than 100 alerts exist. Resolve these, then refresh to see the rest.</p>}
    </>}
    {memberAccess && <form onSubmit={(event) => void decide(event)}><fieldset disabled={busy}>
      <legend>Enroll or revoke a firm member</legend>
      <label>Exact WorkOS user ID<input required pattern="user_.+" maxLength={200} value={subject} onChange={(event) => setSubject(event.target.value)} /></label>
      <p>Current access: {current ? current.granted ? "Enrolled" : "Revoked" : "Not enrolled"}</p>
      <label><input name="granted" type="checkbox" />Enroll this member (leave unchecked to revoke)</label>
      <label>Authority and reason<textarea name="reason" required minLength={10} maxLength={2000} /></label>
      <label><input name="confirmed" type="checkbox" required />I verified the exact user and access decision.</label>
      <button>Save member decision</button>
    </fieldset></form>}
    {memberAccess && <details><summary>Membership history ({members.length})</summary><ol>{members.map((member) => <li key={member.id}>{member.granted ? "Enrolled" : "Revoked"} · {member.subject} · {member.reason} · {member.actor} · {new Date(member.created_at).toLocaleString()}</li>)}</ol></details>}
    <p role="status">{message}</p>
  </details>;
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
