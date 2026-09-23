import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Connection = { id: string; state: string; realm: string; company_name: string; error: string };
type Grant = { id?: string; state?: string; realm?: string; company?: string; prepaid_account_id?: string; expense_account_id?: string; max_amount?: string; expires_at?: string; reason?: string };
type Status = { production_configured: boolean; hosted: boolean; data: { production_connection: Connection | null } };

export function ProductionAccess({ closeId, entity }: { closeId: string; entity: string }) {
  const [status, setStatus] = useState<Status>();
  const [grant, setGrant] = useState<Grant>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [prepaid, setPrepaid] = useState("");
  const [expense, setExpense] = useState("");
  const [limit, setLimit] = useState("");
  const [expiry, setExpiry] = useState("");
  const [reason, setReason] = useState("");
  const [phrase, setPhrase] = useState("");
  const [callback] = useState(() => new URLSearchParams(window.location.hash.slice(1)));
  async function load() {
    try {
      const next = await workspaceRequest(`/${closeId}/quickbooks`);
      setStatus(next);
      setGrant(next.hosted ? await workspaceRequest(`/${closeId}/production-grant`) : undefined);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Production connection status unavailable.");
    }
  }
  useEffect(() => { void load(); }, [closeId]);
  async function connectionAction(action: string) {
    setBusy(true); setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/quickbooks`, {
        action, environment: "production",
        connection_id: status?.data.production_connection?.id ?? "",
        confirmed,
        realm: action === "callback" ? callback.get("qbo_realm") : status?.data.production_connection?.realm,
        code: action === "callback" ? callback.get("qbo_code") : undefined,
        state: action === "callback" ? callback.get("qbo_state") : undefined,
      });
      if (action === "connect") { window.location.assign(result.url); return; }
      if (action === "callback") {
        callback.delete("qbo_code"); callback.delete("qbo_state"); callback.delete("qbo_realm"); callback.delete("qbo_env");
        window.history.replaceState(null, "", window.location.pathname + window.location.search);
      }
      setConfirmed(false); await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Connection action could not be confirmed.");
      await load();
    } finally { setBusy(false); }
  }
  async function grantAction(action: "grant" | "revoke") {
    setBusy(true); setError("");
    try {
      await workspaceRequest(`/${closeId}/production-grant`, {
        action, connection_id: status?.data.production_connection?.id, grant_id: grant?.id,
        prepaid_account_id: prepaid, expense_account_id: expense, max_amount: limit,
        expires_at: expiry ? new Date(expiry).toISOString() : undefined,
        reason, confirmation: phrase,
      });
      setPhrase(""); setConfirmed(false); await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Grant outcome unclear. Refresh status before trying again.");
      await load();
    } finally { setBusy(false); }
  }
  const c = status?.data.production_connection;
  const callbackHere = callback.get("qbo_env") === "production" && callback.has("qbo_code");
  return <details open={callbackHere}>
    <summary>Production QuickBooks authorization and client grant</summary>
    <p>Production posting is always a separate human decision. The connected QuickBooks company name must exactly match the close client <strong>{entity}</strong>. The grant limits this client to two account IDs, a maximum journal amount and an expiry.</p>
    <button disabled={busy} onClick={() => void load()}>Refresh production status</button>
    {error && <p role="alert">{error}</p>}
    {!status ? <p>Loading…</p> : <>
      {!status.hosted && <p>Sign in through the hosted firm workspace to connect or grant production access.</p>}
      {!status.production_configured && <p>Production OAuth is not configured on the server. Local preparation remains available.</p>}
      <p>Company: {c?.company_name || "Not confirmed"} · realm: {c?.realm || "—"} · connection: {c?.state || "not connected"}</p>
      {c?.error && <p role="alert">{c.error}</p>}
      <label><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} /> I checked that this connection belongs to {entity} and company {c?.company_name || "shown by Intuit"}.</label>
      {!c && <button disabled={busy || !confirmed || !status.production_configured} onClick={() => void connectionAction("connect")}>Connect production QuickBooks</button>}
      {c?.state === "authorizing" && callbackHere && <button disabled={busy || !confirmed} onClick={() => void connectionAction("callback")}>Complete production authorization</button>}
      {c?.state === "pending_confirmation" && <button disabled={busy || !confirmed || c.company_name !== entity} onClick={() => void connectionAction("confirm")}>Confirm exact client and company</button>}
      {c?.state === "active" && <button disabled={busy || !confirmed} onClick={() => void connectionAction("refresh")}>Refresh production token</button>}
      {c && <button disabled={busy || !confirmed} onClick={() => void connectionAction("disconnect")}>Disconnect production company locally</button>}
      <h4>Client-specific production grant</h4>
      {grant?.id ? <>
        <p>Grant #{grant.id}: prepaid account {grant.prepaid_account_id}, expense account {grant.expense_account_id}; maximum ${grant.max_amount}; expires {grant.expires_at ? new Date(grant.expires_at).toLocaleString() : "unknown"}.</p>
        <p>Reason: {grant.reason}</p>
        <label>Type REVOKE PRODUCTION GRANT<input value={phrase} onChange={e => setPhrase(e.target.value)} autoComplete="off" /></label>
        <button disabled={busy || phrase !== "REVOKE PRODUCTION GRANT"} onClick={() => void grantAction("revoke")}>Revoke this grant</button>
      </> : <>
        <p>No active grant. A user with client access and posting approval permission must choose exact account IDs from the production Chart of Accounts.</p>
        <label>Prepaid asset account ID<input value={prepaid} onChange={e => setPrepaid(e.target.value)} inputMode="numeric" /></label>
        <label>Expense account ID<input value={expense} onChange={e => setExpense(e.target.value)} inputMode="numeric" /></label>
        <label>Maximum journal amount (USD)<input value={limit} onChange={e => setLimit(e.target.value)} inputMode="decimal" /></label>
        <label>Grant expiry date and time (within 30 days)<input type="datetime-local" value={expiry} onChange={e => setExpiry(e.target.value)} /></label>
        <label>Reason<input value={reason} onChange={e => setReason(e.target.value)} maxLength={2000} /></label>
        <label>Type GRANT PRODUCTION POSTING<input value={phrase} onChange={e => setPhrase(e.target.value)} autoComplete="off" /></label>
        <button disabled={busy || c?.state !== "active" || c.company_name !== entity || phrase !== "GRANT PRODUCTION POSTING"} onClick={() => void grantAction("grant")}>Grant bounded production posting for {entity}</button>
      </>}
    </>}
  </details>;
}
