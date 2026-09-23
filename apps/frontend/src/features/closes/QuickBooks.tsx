import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Connection = {
  id: string;
  state: string;
  realm: string;
  company_name: string;
  error: string;
};
type Snapshot = {
  id: string;
  connection_id: string;
  realm: string;
  captured_at: string;
  started_at: string;
  account_count: number;
  digest: string;
  period?: string;
  source_id?: string;
};
type Status = {
  configured: boolean;
  hosted: boolean;
  data: { connection: Connection | null; snapshots: Snapshot[] };
};
type TransactionSync = {
  id: string;
  period: string;
  state: string;
  attempts: number;
  error: string;
  digest?: string;
  evidence_id?: string;
  counts?: Record<string, number>;
  finished_at?: string;
  started_at?: string;
  completeness?: string;
  latest?: boolean;
};
export function QuickBooks({
  closeId,
  entity,
}: {
  closeId: string;
  entity: string;
}) {
  const [status, setStatus] = useState<Status>();
  const [syncs, setSyncs] = useState<TransactionSync[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [callback] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)),
  );
  useEffect(() => {
    if (callback.has("qbo_code"))
      window.history.replaceState(
        null,
        "",
        window.location.pathname + window.location.search,
      );
  }, [callback]);
  async function load() {
    try {
      const [connection, transactions] = await Promise.all([
        workspaceRequest(`/${closeId}/quickbooks`),
        workspaceRequest(`/${closeId}/quickbooks/entities`),
      ]);
      setStatus(connection);
      setSyncs(transactions);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Connection unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, [closeId]);
  useEffect(() => {
    if (!syncs.some((s) => s.state === "queued" || s.state === "running")) return;
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
  }, [closeId, syncs]);
  async function captureTransactions() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/quickbooks/entities`, {
        connection_id: status?.data.connection?.id,
        confirmed,
      });
      setConfirmed(false);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Transaction refresh could not be queued.");
    } finally {
      setBusy(false);
    }
  }
  async function action(name: string) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/quickbooks`, {
        action: name,
        connection_id: status?.data.connection?.id ?? "",
        confirmed,
        realm:
          name === "callback"
            ? callback.get("qbo_realm")
            : status?.data.connection?.realm,
        code: name === "callback" ? callback.get("qbo_code") : undefined,
        state: name === "callback" ? callback.get("qbo_state") : undefined,
      });
      if (name === "connect") {
        window.location.assign(result.url);
        return;
      }
      if (name === "callback") {
        callback.delete("qbo_code");
        callback.delete("qbo_state");
        callback.delete("qbo_realm");
      }
      setConfirmed(false);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not confirm action.");
      await load();
    } finally {
      setBusy(false);
    }
  }
  async function download(id: string, transactions = false) {
    setError("");
    try {
      const blob = await workspaceRequest(
        transactions ? `/${closeId}/quickbooks/entities/${id}` : `/${closeId}/quickbooks/snapshots/${id}`,
        undefined,
        true,
      );
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `quickbooks-${transactions ? "transactions" : "snapshot"}-${id}.json`;
      a.click();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Download failed.");
    }
  }
  const c = status?.data.connection;
  return (
    <details open={callback.has("qbo_code") && callback.get("qbo_env") !== "production"}>
      <summary>QuickBooks sandbox ledger evidence</summary>
      <p>
        Connect the sandbox company for {entity}. This reader cannot post
        journals. Intuit's accounting permission is broader than read-only; this
        app uses these credentials only for the fixed read operations here.
      </p>
      {error && <p role="alert">{error}</p>}
      <button
        disabled={busy}
        onClick={() => {
          setError("");
          void load();
        }}
      >
        Refresh connection status
      </button>
      {!status && <p>Loading connection…</p>}
      {status && (
        <>
          {!status.hosted && (
            <p>
              Connect from your firm’s signed-in workspace. Local preparation
              works without QuickBooks.
            </p>
          )}
          {!status.configured && (
            <p>
              Sandbox OAuth is not configured. Follow the QuickBooks onboarding
              guide before connecting.
            </p>
          )}
          {c ? (
            <>
              <p>
                {c.company_name || "Company not yet confirmed"} ·{" "}
                {c.realm ? `realm ${c.realm} · ` : ""}
                {c.state.replaceAll("_", " ")}
              </p>
              {c.error && <p role="alert">{c.error}</p>}
              {["exchanging", "refreshing", "reconnect_required"].includes(
                c.state,
              ) && (
                <p>
                  If an exchange was interrupted, disconnect and reconnect.
                  Credentials are never retried blindly.
                </p>
              )}
            </>
          ) : (
            <p>No company connected.</p>
          )}
          {status.hosted && (
            <fieldset disabled={busy}>
              <label>
                <input
                  type="checkbox"
                  checked={confirmed}
                  onChange={(e) => setConfirmed(e.target.checked)}
                />{" "}
                I authorize this action for {entity}
                {c?.realm
                  ? ` and sandbox company ${c.company_name} (${c.realm})`
                  : ""}
                .
              </label>
              {!c && (
                <button
                  disabled={!confirmed || !status.configured}
                  onClick={() => void action("connect")}
                >
                  Connect QuickBooks sandbox
                </button>
              )}
              {c?.state === "authorizing" && callback.has("qbo_code") && callback.get("qbo_env") !== "production" && (
                <button
                  disabled={!confirmed}
                  onClick={() => void action("callback")}
                >
                  Complete authorization
                </button>
              )}
              {c?.state === "pending_confirmation" && (
                <button
                  disabled={!confirmed}
                  onClick={() => void action("confirm")}
                >
                  Confirm this company belongs to the client
                </button>
              )}
              {c?.state === "active" && (
                <button
                  disabled={!confirmed}
                  onClick={() => void action("snapshot")}
                >
                  {busy ? "Reading accounts…" : "Capture account snapshot"}
                </button>
              )}
              {c?.state === "active" && (
                <button disabled={!confirmed} onClick={() => void action("period_snapshot")}>
                  {busy ? "Reading period evidence…" : "Capture this month’s accrual reports and plan coverage"}
                </button>
              )}
              {c?.state === "active" && (
                <button disabled={!confirmed} onClick={() => void captureTransactions()}>
                  Refresh this month’s transactions in background
                </button>
              )}
              {c && (
                <button
                  disabled={!confirmed}
                  onClick={() => void action("disconnect")}
                >
                  Disconnect locally
                </button>
              )}
            </fieldset>
          )}
          <p>
            Disconnecting stops local use and removes stored tokens. Revoke the
            app in Intuit as well to remove provider authorization.
          </p>
          <h3>Captured ledger evidence</h3>
          <p>Transaction refresh runs in the background. It captures provider IDs and revisions for this close month. A partial or failed read is never offered as evidence; review the completed JSON and linked source before using proposed facts.</p>
          {syncs.length === 0 ? <p>No transaction capture requested.</p> : (
            <ul>{syncs.map((s) => <li key={s.id}>
              {s.period} transactions · {s.state.replaceAll("_", " ")} · attempt {s.attempts}/3
              {s.state === "completed" && <> · {s.latest ? "latest completed" : "historical"} · evidence #{s.evidence_id} · {Object.values(s.counts ?? {}).reduce((n, count) => n + count, 0)} records · {s.started_at ? `${new Date(s.started_at).toLocaleString()} to ` : ""}{s.finished_at && new Date(s.finished_at).toLocaleString()} · {s.completeness ? "provider counts checked before and after each type" : "legacy capture; count completeness unverified"} <button onClick={() => void download(s.id, true)}>Download transactions</button></>}
              {s.error && <span role="alert"> · {s.error}</span>}
            </li>)}</ul>
          )}
          <p>Counts and company settings are checked during each transaction capture, but QuickBooks reads are sequential. A later change can make a completed capture stale; refresh before relying on it. Period capture reads Transaction List, General Ledger, Trial Balance and Profit and Loss for this close month on the accrual basis. It links immutable evidence and proposes coverage in Prepare this close. Confirm accrual is appropriate for this client. No facts, exclusions or methods are approved automatically.</p>
          <p>
            These are dated reads of the account directory, including inactive
            accounts. Current balances are not month-end balances, and
            pagination is not an atomic ledger snapshot. Review raw JSON before
            using any values; no accounting facts are confirmed automatically.
          </p>
          {status.data.snapshots.length === 0 ? (
            <p>No account snapshot captured.</p>
          ) : (
            <ul>
              {status.data.snapshots.map((s) => (
                <li key={s.id}>
                  Snapshot #{s.id} · {s.period ? `${s.period} accrual reports · source #${s.source_id}` : "account directory only"} · {s.account_count} accounts ·{" "}
                  {new Date(s.captured_at).toLocaleString()} · realm {s.realm}
                  {s.connection_id !== c?.id || c?.state !== "active"
                    ? " · historical connection"
                    : ""}{" "}
                  <button onClick={() => void download(s.id)}>
                    Download snapshot #{s.id}
                  </button>
                </li>
              ))}
            </ul>
          )}
          <p>
            Up to 20 snapshots per close. A failed refresh leaves earlier
            snapshots labeled with their original capture time.
          </p>
        </>
      )}
    </details>
  );
}
