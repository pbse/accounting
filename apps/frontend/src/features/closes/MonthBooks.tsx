import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { clientPage } from "../../data/clients";
import { monthName } from "../../data/time";
import { workspaceRequest } from "./workspace";

type Connection = {
  id: string;
  environment: "sandbox" | "production";
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
  production_configured: boolean;
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
type TransactionRow = { kind: string; provider_id: string; revision: string; txn_date: string; provider_updated_at: string; currency: string; amount: string; record: unknown; capture_id: string; evidence_id: string };
// A month's books from QuickBooks (docs/books-sources.md, Client page): the company the client's books are read from,
// connected on the client's page, and here this month's captures of it: its accrual reports, its account directory and
// its transactions, each kept with when it was read, with the transactions read from the latest capture.
export function MonthBooks({
  closeId,
  entity,
  period,
}: {
  closeId: string;
  entity: string;
  period: string;
}) {
  const dates = useDates();
  // The connection and its transaction captures, read with the page; the captures are read again every five seconds
  // while one is queued or running.
  const connectionRead = useQuery(workspaceQuery<Status>(`/${closeId}/quickbooks`));
  const syncsRead = useQuery({
    ...workspaceQuery<TransactionSync[]>(`/${closeId}/quickbooks/entities`),
    refetchInterval: (query) => ((query.state.data ?? []).some((s) => s.state === "queued" || s.state === "running") ? 5000 : false),
  });
  const status = connectionRead.data;
  const syncs = syncsRead.data ?? [];
  const [transactions, setTransactions] = useState<TransactionRow[]>([]);
  const [transactionKind, setTransactionKind] = useState("");
  const [transactionOffset, setTransactionOffset] = useState(0);
  const [transactionMore, setTransactionMore] = useState(false);
  const [transactionsLoaded, setTransactionsLoaded] = useState(false);
  const [actionError, setError] = useState("");
  const readFailed = connectionRead.error ?? syncsRead.error;
  const error = actionError || (readFailed ? readFailed.message || "Connection unavailable." : "");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const load = async () => {
    setError("");
    await Promise.all([connectionRead.refetch(), syncsRead.refetch()]);
  };
  useEffect(() => {
    setTransactions([]);
    setTransactionOffset(0);
    setTransactionsLoaded(false);
  }, [closeId]);
  const latestCaptureId = syncs.find(s => s.latest)?.id;
  useEffect(() => {
    setTransactions([]);
    setTransactionOffset(0);
    setTransactionsLoaded(false);
  }, [latestCaptureId]);
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
  async function reviewTransactions(offset: number, kind = transactionKind) {
    setBusy(true);
    try {
      const result = await workspaceRequest(`/${closeId}/quickbooks/entities/transactions?kind=${encodeURIComponent(kind)}&offset=${offset}`);
      setTransactions(result.items);
      setTransactionMore(result.has_more);
      setTransactionOffset(offset);
      setTransactionsLoaded(true);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Transaction index unavailable.");
    } finally {
      setBusy(false);
    }
  }
  async function action(name: string) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/quickbooks`, {
        action: name,
        connection_id: status?.data.connection?.id ?? "",
        confirmed,
        realm: status?.data.connection?.realm,
      });
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
  const production = c?.environment === "production";
  const company = production ? "production company" : "sandbox company";
  return (
    <section id="quickbooks-ledger" aria-label={`${monthName(period)}’s books`}>
      <h2>{monthName(period)}’s books</h2>
      {error && <p role="alert">{error}</p>}
      {!status && <p>Loading…</p>}
      {status && (
        <>
          {c ? (
            <p>
              Read from QuickBooks: {c.company_name || "company not confirmed yet"}
              {c.realm ? ` (company ${c.realm})` : ""} · {c.state.replaceAll("_", " ")}. The connection is managed on <Link to={clientPage(entity)}>{entity}’s page</Link>.
            </p>
          ) : (
            <p>
              No QuickBooks company is connected. Connect one on <Link to={clientPage(entity)}>{entity}’s page</Link>, or label the exports this month uses under Evidence.
            </p>
          )}
          {c?.error && <p role="alert">{c.error}</p>}
          {status.hosted && c?.state === "active" && (
            <fieldset disabled={busy}>
              <label>
                <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I read {entity}’s {company} {c.company_name} ({c.realm}) for {monthName(period)}.
              </label>
              <div className="setting-actions">
                <button disabled={!confirmed} onClick={() => void action("period_snapshot")}>
                  {busy ? "Reading the month…" : "Capture this month’s accrual reports and plan coverage"}
                </button>
                <button disabled={!confirmed} onClick={() => void action("snapshot")}>
                  {busy ? "Reading accounts…" : "Capture the account directory"}
                </button>
                <button disabled={!confirmed} onClick={() => void captureTransactions()}>
                  Refresh this month’s transactions in background
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setError("");
                    void load();
                  }}
                >
                  Refresh
                </button>
              </div>
            </fieldset>
          )}
          {!status.hosted && <p className="note">Read the client’s QuickBooks company from your firm’s signed-in workspace. Local preparation works without QuickBooks.</p>}
          <h3>Captured ledger evidence</h3>
          <p>For the active connection, the worker checks unsigned-off current and prior-month closes automatically every 24 hours. It retries interrupted checks and records failures here. Identical captures reuse the current evidence; changed captures reopen dependent sandbox approvals. A partial read is never offered as complete evidence.</p>
          {syncs.length === 0 ? <p>No transaction capture requested.</p> : (
            <ul>{syncs.map((s) => <li key={s.id}>
              {s.period} transactions · {s.state.replaceAll("_", " ")} · attempt {s.attempts}/3
              {s.state === "completed" && <> · {s.latest ? "latest completed" : "historical"} · evidence #{s.evidence_id} · {Object.values(s.counts ?? {}).reduce((n, count) => n + count, 0)} records · {s.started_at ? `${dates.dateTime(s.started_at)} to ` : ""}{s.finished_at && dates.dateTime(s.finished_at)} · {s.completeness || "legacy capture; count completeness unverified"} <button onClick={() => void download(s.id, true)}>Download transactions</button></>}
              {s.state === "unchanged" && <> · no source change · evidence #{s.evidence_id} · checked {s.finished_at && dates.dateTime(s.finished_at)}{s.completeness && ` · ${s.completeness}`}</>}
              {s.error && <span role="alert"> · {s.error}</span>}
            </li>)}</ul>
          )}
          <h4>Review indexed transactions</h4>
          <p>Rows come from the latest completed capture and retain the provider ID, revision and exact source record. Values here are evidence, not confirmed accounting facts. Older captures need a new refresh to populate this index.</p>
          <label>Transaction type <select value={transactionKind} onChange={e => { setTransactionKind(e.target.value); setTransactions([]); setTransactionOffset(0); setTransactionsLoaded(false); }}><option value="">All types</option>{Object.keys(syncs.find(s => s.latest)?.counts ?? {}).sort().map(kind => <option key={kind} value={kind}>{kind}</option>)}</select></label>
          <button disabled={busy} onClick={() => void reviewTransactions(0)}>Show latest transactions</button>
          {transactions.length > 0 && <><ul>{transactions.map(row => <li key={`${row.kind}-${row.provider_id}`}>{row.txn_date} · {row.kind} #{row.provider_id} · revision {row.revision} · {row.amount ? `${row.currency || "currency unverified"} ${row.amount}` : "amount not provided"} · source #{row.evidence_id}<details><summary>Inspect exact provider record</summary><pre>{JSON.stringify(row.record, null, 2)}</pre></details></li>)}</ul><button disabled={busy || transactionOffset === 0} onClick={() => void reviewTransactions(Math.max(0, transactionOffset - 100))}>Previous 100</button><button disabled={busy || !transactionMore} onClick={() => void reviewTransactions(transactionOffset + 100)}>Next 100</button></>}
          {transactionsLoaded && transactions.length === 0 && <p>The selected capture has no indexed rows for this filter, or has not been refreshed since indexing was added.</p>}
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
                  {dates.dateTime(s.captured_at)} · realm {s.realm}
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
    </section>
  );
}
