import { useState } from "react";
import { workspaceRequest } from "./workspace";

type Candidate = { id: string; date: string; amount: string; vendor_id: string };
type Correction = { state: string; digest?: string; bill_id?: string; treatment?: string; reason?: string; target_account_id?: string; target_line_id?: string; target_account_name?: string; before_bill?: { TxnDate: string; SyncToken: string; TotalAmt: string; VendorRef: { value: string }; Line: { Id: string; Amount: string; Description?: string; AccountBasedExpenseLineDetail: { AccountRef: { value: string } } }[] }; approved_by?: string; approval_expires_at?: string; readback?: unknown; error?: string };

export function BillCorrection({ closeId }: { closeId: string }) {
  const [bills, setBills] = useState<Candidate[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [manualId, setManualId] = useState("");
  const [selected, setSelected] = useState("");
  const [treatment, setTreatment] = useState("inconsistent_coding");
  const [reason, setReason] = useState("");
  const [account, setAccount] = useState("");
  const [lineId, setLineId] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [item, setItem] = useState<Correction>();
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    setBusy(true); setMessage("");
    try {
      const result = await workspaceRequest(`/${closeId}/quickbooks/bills/candidates`);
      setBills(result.bills);
      setHasMore(result.has_more === true);
      setMessage(result.message ?? (result.has_more ? "Showing the first 100 Bills. Use the transaction capture for older Bill IDs." : ""));
    } catch (error) { setMessage(error instanceof Error ? error.message : "Bill candidates unavailable."); }
    finally { setBusy(false); }
  }
  async function select(id: string) {
    setSelected(id); setItem(undefined); setLineId(""); setConfirmed(false); setMessage("");
    if (!id) return;
    try { const current = await workspaceRequest(`/${closeId}/quickbooks/bills/${encodeURIComponent(id)}/correction`); setItem(current); setLineId(current.target_line_id ?? ""); }
    catch (error) { setMessage(error instanceof Error ? error.message : "Correction status unavailable."); }
  }
  async function run(action: string) {
    if (!selected) return;
    setBusy(true); setMessage("");
    try {
      const next = await workspaceRequest(`/${closeId}/quickbooks/bills/${encodeURIComponent(selected)}/correction`, { action, digest: item?.digest, treatment, reason, target_account_id: account, target_line_id: lineId, confirmed });
      setItem(next); setConfirmed(false);
      setMessage(action === "execute" ? "QuickBooks readback verified. Refresh the transaction capture before preparing dependent work." : action === "verify" ? "Canonical Bill readback verified." : "Saved exact correction state.");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Bill correction unavailable."); }
    finally { setBusy(false); }
  }
  return <details onToggle={event => { if (event.currentTarget.open && bills.length === 0) void load(); }}>
    <summary>Review a Bill expense-account correction</summary>
    <p>For an unpaid USD Bill with up to ten simple expense lines. Select exactly one line and target account; the other lines must read back unchanged. This posts only to the configured QuickBooks sandbox. No automatic retry follows an uncertain write.</p>
    <button type="button" disabled={busy} onClick={() => void load()}>Refresh Bill candidates</button>
    <label>Bill from the latest transaction capture<select value={selected} onChange={event => void select(event.target.value)}><option value="">Choose a Bill</option>{selected && !bills.some(b => b.id === selected) && <option value={selected}>Bill #{selected}</option>}{bills.map(b => <option key={b.id} value={b.id}>#{b.id} · {b.date} · ${b.amount} · vendor #{b.vendor_id}</option>)}</select></label>
    {hasMore && <p><label>Older Bill ID from the downloaded transaction capture<input inputMode="numeric" pattern="[0-9]+" value={manualId} onChange={event => setManualId(event.target.value)} /></label><button type="button" disabled={busy || !/^[0-9]+$/.test(manualId)} onClick={() => void select(manualId)}>Review this captured Bill</button></p>}
    {selected && <>
      {item?.state && item.state !== "not_prepared" && <p>State: {item.state}. Version: {item.before_bill?.SyncToken}. Line #{item.target_line_id} → {item.target_account_name} (#{item.target_account_id}). {item.approved_by && `Approved by ${item.approved_by}.`}</p>}
      {item?.error && <p role="alert">{item.error}</p>}
      {(!item || item.state === "not_prepared" || item.state === "preview" || item.state === "approved") && <>
        <label>Exception treatment<select value={treatment} onChange={event => setTreatment(event.target.value)}><option value="inconsistent_coding">Inconsistent expense coding</option><option value="uncategorized_expense">Uncategorized expense</option></select></label>
        <label>Accountant reason and source reference<textarea value={reason} onChange={event => setReason(event.target.value)} maxLength={2000} /></label>
        <label>Bill expense line<select value={lineId} onChange={event => setLineId(event.target.value)}><option value="">Choose the exact line</option>{item?.before_bill?.Line?.map(line => <option key={line.Id} value={line.Id}>#{line.Id} · ${line.Amount} · current account #{line.AccountBasedExpenseLineDetail.AccountRef.value}{line.Description ? ` · ${line.Description}` : ""}</option>)}</select></label>
        <label>Target QuickBooks expense account ID<input value={account} onChange={event => setAccount(event.target.value)} inputMode="numeric" /></label>
        <label><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />I reviewed the Bill, supporting capture, treatment, and exact target account.</label>
        <button type="button" disabled={busy || !confirmed || reason.trim().length < 5 || !lineId || !account} onClick={() => void run("preview")}>Preview exact correction</button>
      </>}
      {item?.state === "preview" && <button type="button" disabled={busy || !confirmed} onClick={() => void run("approve")}>Approve this Bill correction for 10 minutes</button>}
      {item?.state === "approved" && <button type="button" disabled={busy || !confirmed} onClick={() => void run("execute")}>Update Bill account once in sandbox</button>}
      {(item?.state === "executing" || item?.state === "unknown" || item?.state === "verified") && <button type="button" disabled={busy} onClick={() => void run("verify")}>Read back Bill from QuickBooks</button>}
    </>}
    {message && <p role="status">{message}</p>}
  </details>;
}
