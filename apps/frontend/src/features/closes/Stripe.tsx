import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Status = {
  connection: { id: string; state: string; livemode: boolean; key_last4: string; account_id: string; account_name: string } | null;
  runs: { id: string; period: string; report: string; state: string; error: string; evidence_id?: string; created_at: string }[];
};

const reports: Record<string, string> = {
  payout_reconciliation: "Payout reconciliation (automatic payouts)",
  payouts: "Every payout (manual, instant or mixed)",
  activity: "Balance change from activity",
};

// A read-only Stripe connection brings the month's itemized reports into the close, as if uploaded.
export function Stripe({ closeId, entity }: { closeId: string; entity: string }) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [key, setKey] = useState("");
  const [report, setReport] = useState("payout_reconciliation");
  async function load() {
    try {
      setStatus(await workspaceRequest(`/${closeId}/stripe`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Stripe status unavailable.");
    }
  }
  useEffect(() => {
    void load();
  }, [closeId]);
  async function act(action: string) {
    setBusy(true);
    setError("");
    try {
      setStatus(await workspaceRequest(`/${closeId}/stripe`, { action, key, connection_id: status?.connection?.id ?? "", confirmed, report }));
      setKey("");
      setConfirmed(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Stripe action could not be confirmed.");
      await load();
    } finally {
      setBusy(false);
    }
  }
  const c = status?.connection;
  const confirm = (
    <label>
      <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I confirm this Stripe account belongs to {entity}.
    </label>
  );
  return (
    <details>
      <summary>Stripe reports</summary>
      <p>
        Reads the month's itemized payout reconciliation, Payouts or balance activity report from the client's Stripe account and adds it to this
        close as evidence, like an uploaded export. It never changes anything in Stripe except asking it to generate the report. For manual or
        instant payouts, capture every payout and the balance activity, then trace them together.
      </p>
      {error && <p role="alert">{error}</p>}
      {!status ? null : !c ? (
        <>
          <p className="note">
            In Stripe, create a restricted key with read access to reports and account details and write access to report runs. Secret keys are
            refused. The key is encrypted and never shown again.
          </p>
          <label>
            Restricted key
            <input value={key} onChange={(e) => setKey(e.target.value.trim())} autoComplete="off" spellCheck={false} />
          </label>
          {confirm}
          <button disabled={busy || !confirmed || !key} onClick={() => void act("connect")}>
            Check key and account
          </button>
        </>
      ) : c.state === "unconfirmed" ? (
        <>
          <p>
            The key belongs to Stripe account <strong>{c.account_name || c.account_id}</strong> ({c.account_id}, {c.livemode ? "live" : "test"} mode, key
            ending {c.key_last4}). Use it only if this is {entity}'s account.
          </p>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void act("confirm")}>
            Use this account
          </button>
        </>
      ) : (
        <>
          <p>
            Connected to <strong>{c.account_name || c.account_id}</strong> ({c.livemode ? "live" : "test"} mode, key ending {c.key_last4}).
          </p>
          <label>
            Report
            <select value={report} onChange={(e) => setReport(e.target.value)}>
              {Object.entries(reports).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
          </label>
          {confirm}
          <button disabled={busy || !confirmed} onClick={() => void act("capture")}>
            {busy ? "Asking Stripe…" : "Capture this month's report"}
          </button>
        </>
      )}
      {(status?.runs.length ?? 0) > 0 && (
        <ul>
          {status!.runs.map((r) => (
            <li key={r.id}>
              {r.period} · {reports[r.report] ?? r.report}: {r.state === "saved" ? (r.report === "activity" ? "saved as evidence; choose it as the activity report when tracing the Payouts report" : "saved as evidence; review it in Work") : r.state === "failed" ? `failed. ${r.error}` : "Stripe is preparing the report; it is saved within a few minutes"}
            </li>
          ))}
        </ul>
      )}
      {c && (
        <>
          <button disabled={busy} onClick={() => void load()}>
            Refresh status
          </button>
          <button disabled={busy || !confirmed} onClick={() => void act("disconnect")}>
            Disconnect
          </button>
        </>
      )}
    </details>
  );
}
