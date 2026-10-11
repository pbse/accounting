import { workspaceKey, workspaceQuery } from "../../data/queries";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { monthName, useDates } from "../../data/time";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { clientPage } from "../../data/clients";
import { workspaceRequest } from "./workspace";

type Balance = { account_id: string; code: string; name: string; class: string; debit: string; credit: string; ytd_debit: string; ytd_credit: string };
type Status = {
  configured: boolean;
  hosted: boolean;
  connection: { id: string; state: string; tenant_name: string; error: string } | null;
  snapshots: { id: string; period: string; captured_at: string; digest: string }[];
  latest: { currency: string; date: string; rows: Balance[]; transactions?: { unread: string; counts?: Record<string, number> } } | { error: string } | null;
};

// A month's Trial Balance from Xero (docs/books-sources.md, Client page): the organisation the client's books are read
// from, connected on the client's page, and here this month's captures of it, the latest one's balances, and the
// reviewed recode of a bill line under the client's Xero grant.
export function Xero({ closeId, entity, period }: { closeId: string; entity: string; period: string }) {
  const dates = useDates();
  const queryClient = useQueryClient();
  const read = useQuery(workspaceQuery<Status>(`/${closeId}/xero`));
  const status = read.data;
  // An action answers with the new status, which replaces the read one.
  const setStatus = (next: Status) => queryClient.setQueryData(workspaceKey(`/${closeId}/xero`), next);
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "Xero status unavailable." : "");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const load = async () => void (await read.refetch());
  async function capture() {
    setBusy(true);
    setError("");
    try {
      setStatus(await workspaceRequest(`/${closeId}/xero`, { action: "snapshot", connection_id: status?.connection?.id ?? "", confirmed }));
      setConfirmed(false);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The Trial Balance was not captured.");
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
  // A month with no Xero connection and nothing captured has nothing to show here.
  if (status && !c && status.snapshots.length === 0) return null;
  return (
    <section id="xero" aria-label={`${monthName(period)}’s Trial Balance from Xero`}>
      <h2>{monthName(period)}’s Trial Balance from Xero</h2>
      <p>
        {c?.state === "active" ? (
          <>
            Read from <strong>{c.tenant_name}</strong>.{" "}
          </>
        ) : c ? (
          <>Xero is {c.state.replaceAll("_", " ")}. </>
        ) : null}
        The connection is managed on <Link to={clientPage(entity)}>{entity}’s page</Link>. Xero data is not used to train models.
      </p>
      {error && <p role="alert">{error}</p>}
      {c && c.error && <p role="status">Last attempt: {c.error}</p>}
      {status?.hosted && c?.state === "active" && (
        <>
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I read {entity}’s Xero organisation for {monthName(period)}.
          </label>
          <button disabled={busy || !confirmed} onClick={() => void capture()}>
            {busy ? "Reading Xero…" : "Capture month-end Trial Balance"}
          </button>
          <XeroBillCorrection closeId={closeId} />
        </>
      )}
      {latest && "error" in latest && <p role="alert">The latest snapshot could not be read: {latest.error}</p>}
      {latest && "rows" in latest && (
        <>
          <p>
            Trial Balance as at {latest.date}, in {latest.currency || "the organisation's base currency"}. YTD columns are Xero's: the balance at that date for asset, liability and equity accounts, and the movement since the start of the financial year for revenue and expense accounts.
          </p>
          {latest.transactions?.counts ? (
            <p className="note">
              Transactions read with it:{" "}
              {Object.entries(latest.transactions.counts)
                .map(([kind, n]) => `${n} ${kind.replace(/([a-z])([A-Z])/g, "$1 $2").toLowerCase()}`)
                .join(", ")}
              .
            </p>
          ) : (
            latest.transactions?.unread && <p role="status">Transactions were not read: {latest.transactions.unread}</p>
          )}
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
          Snapshot #{s.id} for {s.period}, captured {dates.dateTime(s.captured_at)}.{" "}
          <button onClick={() => void download(s.id)}>Download exact JSON</button>
        </p>
      ))}
    </section>
  );
}

type XeroLine = { LineItemID: string; Description?: string; AccountCode?: string; LineAmount: number | string };
type XeroFix = { state: string; digest?: string; target_line_id?: string; target_account_id?: string; target_account_code?: string; reason?: string; target_account_name?: string; rule_reference?: string; approved_by?: string; error?: string; before_bill?: { LineItems: XeroLine[] } };

// One reviewed recode of one Xero bill line, under the client's Xero grant.
function XeroBillCorrection({ closeId }: { closeId: string }) {
  const [bills, setBills] = useState<{ id: string; number: string; date: string; total: string; currency: string }[]>([]);
  const [selected, setSelected] = useState("");
  const [item, setItem] = useState<XeroFix>();
  const [lineId, setLineId] = useState("");
  const [account, setAccount] = useState("");
  const [reason, setReason] = useState("");
  const [rule, setRule] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const base = `/${closeId}/xero/bills`;
  const panel = useRef<HTMLDetailsElement>(null);
  const { hash } = useLocation();
  useEffect(() => {
    const asked = /^#correct-xero-([\w-]+)$/.exec(hash)?.[1];
    if (!asked || !panel.current) return;
    panel.current.open = true;
    panel.current.scrollIntoView({ block: "start" });
    void select(asked);
  }, [hash]);
  async function load() {
    try {
      const result = await workspaceRequest(`${base}/candidates`);
      setBills(result.items);
      setMessage(result.message ?? "");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Candidates unavailable.");
    }
  }
  async function select(id: string) {
    setSelected(id); setItem(undefined); setLineId(""); setConfirmed(false); setMessage("");
    if (!id) return;
    try {
      const current = await workspaceRequest(`${base}/${encodeURIComponent(id)}/correction`);
      setItem(current);
      setLineId(current.target_line_id ?? "");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Correction status unavailable.");
    }
  }
  async function run(action: string) {
    setBusy(true);
    setMessage("");
    try {
      setItem(await workspaceRequest(`${base}/${encodeURIComponent(selected)}/correction`, { action, digest: item?.digest, reason, rule_reference: rule, target_line_id: lineId, target_account_id: account, confirmed }));
      setConfirmed(false);
      setMessage(action === "execute" || action === "verify" ? "Xero readback verified. Capture Xero again before relying on its figures." : "Saved exact correction state.");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Correction outcome unclear. Refresh before any retry.");
    } finally {
      setBusy(false);
    }
  }
  const editable = !item || item.state === "not_prepared" || item.state === "preview" || item.state === "approved";
  return (
    <details ref={panel} onToggle={(e) => { if (e.currentTarget.open && bills.length === 0) void load(); }}>
      <summary>Recode a Xero bill line</summary>
      <p>For an approved bill in the close month with nothing paid or credited, no tax and up to ten lines. Choose one line and a new expense account under the accountant's cited rule; both accounts must be in a Xero grant that covers bill corrections. Everything else must read back unchanged, and an uncertain update is never retried.</p>
      <label>
        Bill from the latest Xero capture
        <select value={selected} onChange={(e) => void select(e.target.value)}>
          <option value="">Choose one</option>
          {selected && !bills.some((b) => b.id === selected) && <option value={selected}>Bill {selected}</option>}
          {bills.map((b) => <option key={b.id} value={b.id}>{b.number || b.id} · {b.date} · {b.currency} {b.total}</option>)}
        </select>
      </label>
      {item && item.state !== "not_prepared" && <p>State: {item.state}. Line {item.target_line_id} → {item.target_account_name} ({item.target_account_code}, account ID {item.target_account_id}). Reason: {item.reason} Rule: {item.rule_reference}. {item.approved_by && `Approved by ${item.approved_by}.`}</p>}
      {item?.error && <p role="alert">{item.error}</p>}
      {selected && editable && (
        <>
          <label>
            Line
            <select value={lineId} onChange={(e) => setLineId(e.target.value)}>
              <option value="">Choose the exact line</option>
              {item?.before_bill?.LineItems.map((l) => <option key={l.LineItemID} value={l.LineItemID}>{l.LineAmount} · account {l.AccountCode}{l.Description ? ` · ${l.Description}` : ""}</option>)}
            </select>
          </label>
          <label>
            New Xero expense account ID
            <input value={account} onChange={(e) => setAccount(e.target.value)} />
          </label>
          <label>
            Accountant reason and source reference
            <textarea value={reason} maxLength={2000} onChange={(e) => setReason(e.target.value)} />
          </label>
          <label>
            Accountant's coding rule it follows
            <input value={rule} maxLength={200} onChange={(e) => setRule(e.target.value)} />
          </label>
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> I reviewed the bill, the capture, the rule and the exact new account.
          </label>
          <button disabled={busy || !confirmed || !lineId || !account || reason.trim().length < 5 || rule.trim().length < 5} onClick={() => void run("preview")}>Preview exact correction</button>
        </>
      )}
      {item?.state === "preview" && <button disabled={busy || !confirmed} onClick={() => void run("approve")}>Approve this correction for 10 minutes</button>}
      {item?.state === "approved" && <button disabled={busy || !confirmed} onClick={() => void run("execute")}>Update the bill once</button>}
      {(item?.state === "executing" || item?.state === "unknown" || item?.state === "verified") && <button disabled={busy} onClick={() => void run("verify")}>Read back from Xero</button>}
      {message && <p role="status">{message}</p>}
    </details>
  );
}
