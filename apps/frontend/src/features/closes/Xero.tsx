import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Tenant = { tenantId: string; tenantName: string };
type Balance = { account_id: string; code: string; name: string; class: string; debit: string; credit: string; ytd_debit: string; ytd_credit: string };
type Status = {
  configured: boolean;
  hosted: boolean;
  connection: { id: string; state: string; tenant_name: string; tenants: Tenant[]; error: string } | null;
  snapshots: { id: string; period: string; captured_at: string; digest: string }[];
  latest: { currency: string; date: string; rows: Balance[] } | { error: string } | null;
};

export function Xero({ closeId, entity }: { closeId: string; entity: string }) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [tenant, setTenant] = useState("");
  const [callback] = useState(() => new URLSearchParams(window.location.hash.slice(1)));
  useEffect(() => {
    if (callback.has("xero_code")) window.history.replaceState(null, "", window.location.pathname + window.location.search);
  }, [callback]);
  async function load() {
    try {
      setStatus(await workspaceRequest(`/${closeId}/xero`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Xero status unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, [closeId]);
  async function action(name: string) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/xero`, {
        action: name,
        connection_id: status?.connection?.id ?? "",
        confirmed,
        tenant_id: name === "confirm" ? tenant : undefined,
        code: name === "callback" ? callback.get("xero_code") : undefined,
        state: name === "callback" ? callback.get("xero_state") : undefined,
      });
      if (name === "connect") {
        window.location.assign(result.url);
        return;
      }
      if (name === "callback") {
        callback.delete("xero_code");
        callback.delete("xero_state");
      }
      setConfirmed(false);
      setStatus(result);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The Xero action did not complete.");
      await load();
    } finally {
      setBusy(false);
    }
  }
  async function download(id: string) {
    try {
      const url = URL.createObjectURL(await workspaceRequest(`/${closeId}/xero/snapshots/${id}`, undefined, true));
      const link = document.createElement("a");
      link.href = url;
      link.download = `xero-trial-balance-${id}.json`;
      link.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Download failed.");
    }
  }
  const c = status?.connection;
  const latest = status?.latest;
  const confirm = (
    <label>
      <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I am authorised to connect {entity}'s Xero organisation for read-only month-end evidence.
    </label>
  );
  return (
    <details open={callback.has("xero_code")}>
      <summary>Xero month-end Trial Balance</summary>
      <p>Read-only: this reads the organisation, its chart of accounts and the Trial Balance at month-end. It never writes to Xero, and Xero data is not used to train models.</p>
      {error && <p role="alert">{error}</p>}
      {!status ? null : !status.configured ? (
        <p className="empty">Xero is not configured on this server. Set the Xero app's client ID, secret and callback URL first.</p>
      ) : !status.hosted ? (
        <p className="empty">Connecting Xero needs a signed-in firm session with integrations access.</p>
      ) : !c ? (
        <>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void action("connect")}>
            Connect Xero
          </button>
        </>
      ) : c.state === "authorizing" && callback.has("xero_code") ? (
        <>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void action("callback")}>
            Finish connecting Xero
          </button>
        </>
      ) : c.state === "choose_tenant" ? (
        <fieldset>
          <legend>Which Xero organisation belongs to {entity}?</legend>
          {c.tenants.map((t) => (
            <label key={t.tenantId}>
              <input type="radio" name="xero-tenant" value={t.tenantId} checked={tenant === t.tenantId} onChange={() => setTenant(t.tenantId)} /> {t.tenantName}
            </label>
          ))}
          {confirm}
          <button disabled={busy || !confirmed || !tenant} onClick={() => void action("confirm")}>
            Use this organisation
          </button>
        </fieldset>
      ) : c.state === "active" ? (
        <>
          <p>
            Connected to <strong>{c.tenant_name}</strong>.
          </p>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void action("snapshot")}>
            {busy ? "Reading Xero…" : "Capture month-end Trial Balance"}
          </button>
        </>
      ) : (
        <>
          <p role="status">
            Connection {c.state.replaceAll("_", " ")}. {c.error} Disconnect, then connect again.
          </p>
          {confirm}
        </>
      )}
      {c && c.error && c.state === "active" && <p role="status">Last attempt: {c.error}</p>}
      {latest && "error" in latest && <p role="alert">The latest snapshot could not be read: {latest.error}</p>}
      {latest && "rows" in latest && (
        <>
          <p>
            Trial Balance as at {latest.date}, in {latest.currency || "the organisation's base currency"}. YTD columns are Xero's: the balance at that date for asset, liability and equity accounts, and the movement since the start of the financial year for revenue and expense accounts.
          </p>
          <table>
            <thead>
              <tr>
                <th>Code</th>
                <th>Account</th>
                <th>Class</th>
                <th>Debit</th>
                <th>Credit</th>
                <th>YTD Debit</th>
                <th>YTD Credit</th>
              </tr>
            </thead>
            <tbody>
              {latest.rows.map((row) => (
                <tr key={row.account_id}>
                  <td>{row.code}</td>
                  <td>{row.name}</td>
                  <td>{row.class || "Not in chart"}</td>
                  <td>{row.debit}</td>
                  <td>{row.credit}</td>
                  <td>{row.ytd_debit}</td>
                  <td>{row.ytd_credit}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {status?.snapshots.map((s) => (
        <p key={s.id}>
          Snapshot #{s.id} for {s.period}, captured {new Date(s.captured_at).toLocaleString()}.{" "}
          <button onClick={() => void download(s.id)}>Download exact JSON</button>
        </p>
      ))}
      {c && (
        <button disabled={busy || !confirmed} onClick={() => void action("disconnect")}>
          Disconnect Xero
        </button>
      )}
    </details>
  );
}
