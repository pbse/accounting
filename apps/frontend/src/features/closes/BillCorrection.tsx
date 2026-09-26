import { useEffect, useRef, useState } from "react";
import { useLocation } from "react-router";
import { workspaceRequest } from "./workspace";

type Candidate = { id: string; date: string; amount: string; vendor_id: string };
type Detail = { AccountRef: { value: string } };
type Correction = { state: string; digest?: string; bill_id?: string; treatment?: string; reason?: string; rule_reference?: string; target_account_id?: string; target_line_id?: string; target_account_name?: string; before_bill?: { TxnDate: string; SyncToken: string; TotalAmt: string; Line: { Id: string; Amount: string; Description?: string; AccountBasedExpenseLineDetail?: Detail; DepositLineDetail?: Detail }[] }; approved_by?: string; approval_expires_at?: string; readback?: unknown; error?: string };

type Kind = "bills" | "purchases" | "deposits";
const kinds: Record<Kind, { title: string; noun: string; about: string; account: string; treatments: [string, string][]; grant: string }> = {
  bills: { title: "Review a Bill expense-account correction", noun: "Bill", about: "For an unpaid Bill in the client's currency with up to ten simple expense lines.", account: "expense", treatments: [["inconsistent_coding", "Inconsistent expense coding"], ["uncategorized_expense", "Uncategorized expense"]], grant: "Bill corrections" },
  purchases: { title: "Recode an expense line (card charge, check or cash)", noun: "expense", about: "For an expense in the client's currency with up to ten simple expense lines, no tax and nothing linked.", account: "expense", treatments: [["inconsistent_coding", "Inconsistent expense coding"], ["uncategorized_expense", "Uncategorized expense"]], grant: "expense recodes" },
  deposits: { title: "Recode an income deposit line", noun: "deposit", about: "For a deposit in the client's currency whose lines post straight to income: no deposited payments, cash back or tax. The line and its new account must both be income.", account: "income", treatments: [["uncategorized_income", "Uncategorized income"], ["inconsistent_coding", "Inconsistent income coding"]], grant: "deposit recodes" },
};

export function BillCorrection({ closeId, kind = "bills" }: { closeId: string; kind?: Kind }) {
  const k = kinds[kind];
  const [bills, setBills] = useState<Candidate[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [manualId, setManualId] = useState("");
  const [selected, setSelected] = useState("");
  const [treatment, setTreatment] = useState(k.treatments[0][0]);
  const [reason, setReason] = useState("");
  const [rule, setRule] = useState("");
  const [account, setAccount] = useState("");
  const [lineId, setLineId] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [item, setItem] = useState<Correction>();
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const panel = useRef<HTMLDetailsElement>(null);
  const { hash } = useLocation();
  useEffect(() => {
    const asked = new RegExp(`^#correct-${kind}-([0-9]+)$`).exec(hash)?.[1];
    if (!asked || !panel.current) return;
    panel.current.open = true;
    panel.current.scrollIntoView({ block: "start" });
    void select(asked);
  }, [hash]);
  async function load() {
    setBusy(true); setMessage("");
    try {
      const result = await workspaceRequest(`/${closeId}/quickbooks/${kind}/candidates`);
      setBills(result.items);
      setHasMore(result.has_more === true);
      setMessage(result.message ?? (result.has_more ? `Showing the first 100. Use the transaction capture for older ${k.noun} IDs.` : ""));
    } catch (error) { setMessage(error instanceof Error ? error.message : "Candidates unavailable."); }
    finally { setBusy(false); }
  }
  async function select(id: string) {
    setSelected(id); setItem(undefined); setLineId(""); setConfirmed(false); setMessage("");
    if (!id) return;
    try { const current = await workspaceRequest(`/${closeId}/quickbooks/${kind}/${encodeURIComponent(id)}/correction`); setItem(current); setLineId(current.target_line_id ?? ""); }
    catch (error) { setMessage(error instanceof Error ? error.message : "Correction status unavailable."); }
  }
  async function run(action: string) {
    if (!selected) return;
    setBusy(true); setMessage("");
    try {
      const next = await workspaceRequest(`/${closeId}/quickbooks/${kind}/${encodeURIComponent(selected)}/correction`, { action, digest: item?.digest, treatment, reason, rule_reference: rule, target_account_id: account, target_line_id: lineId, confirmed });
      setItem(next); setConfirmed(false);
      setMessage(action === "execute" ? "QuickBooks readback verified. Refresh the transaction capture before preparing dependent work." : action === "verify" ? "Canonical readback verified." : "Saved exact correction state.");
    } catch (error) { setMessage(error instanceof Error ? error.message : "Correction unavailable."); }
    finally { setBusy(false); }
  }
  return <details ref={panel} onToggle={event => { if (event.currentTarget.open && bills.length === 0) void load(); }}>
    <summary>{k.title}</summary>
    <p>{k.about} Select exactly one line and a new {k.account} account, under the accountant's cited rule; everything else must read back unchanged. It writes to the client's production QuickBooks company under a grant that covers {k.grant} (both the line's current and new accounts must be granted), or to the configured sandbox company in development. No automatic retry follows an uncertain write.</p>
    <button type="button" disabled={busy} onClick={() => void load()}>Refresh {k.noun} candidates</button>
    <label>{k.noun[0].toUpperCase() + k.noun.slice(1)} from the latest transaction capture<select value={selected} onChange={event => void select(event.target.value)}><option value="">Choose one</option>{selected && !bills.some(b => b.id === selected) && <option value={selected}>#{selected}</option>}{bills.map(b => <option key={b.id} value={b.id}>#{b.id} · {b.date} · ${b.amount}{b.vendor_id ? ` · payee #${b.vendor_id}` : ""}</option>)}</select></label>
    {hasMore && <p><label>Older ID from the downloaded transaction capture<input inputMode="numeric" pattern="[0-9]+" value={manualId} onChange={event => setManualId(event.target.value)} /></label><button type="button" disabled={busy || !/^[0-9]+$/.test(manualId)} onClick={() => void select(manualId)}>Review this captured {k.noun}</button></p>}
    {selected && <>
      {item?.state && item.state !== "not_prepared" && <p>State: {item.state}. Version: {item.before_bill?.SyncToken}. Line #{item.target_line_id} → {item.target_account_name} (#{item.target_account_id}). Rule: {item.rule_reference}. {item.approved_by && `Approved by ${item.approved_by}.`}</p>}
      {item?.error && <p role="alert">{item.error}</p>}
      {(!item || item.state === "not_prepared" || item.state === "preview" || item.state === "approved") && <>
        <label>Exception treatment<select value={treatment} onChange={event => setTreatment(event.target.value)}>{k.treatments.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
        <label>Accountant reason and source reference<textarea value={reason} onChange={event => setReason(event.target.value)} maxLength={2000} /></label>
        <label>Accountant's coding rule it follows<input value={rule} onChange={event => setRule(event.target.value)} maxLength={200} /></label>
        <label>Line<select value={lineId} onChange={event => setLineId(event.target.value)}><option value="">Choose the exact line</option>{item?.before_bill?.Line?.map(line => <option key={line.Id} value={line.Id}>#{line.Id} · ${line.Amount} · current account #{(line.AccountBasedExpenseLineDetail ?? line.DepositLineDetail)?.AccountRef.value}{line.Description ? ` · ${line.Description}` : ""}</option>)}</select></label>
        <label>New QuickBooks {k.account} account ID<input value={account} onChange={event => setAccount(event.target.value)} inputMode="numeric" /></label>
        <label><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />I reviewed the {k.noun}, supporting capture, treatment, rule and exact new account.</label>
        <button type="button" disabled={busy || !confirmed || reason.trim().length < 5 || rule.trim().length < 5 || !lineId || !account} onClick={() => void run("preview")}>Preview exact correction</button>
      </>}
      {item?.state === "preview" && <button type="button" disabled={busy || !confirmed} onClick={() => void run("approve")}>Approve this correction for 10 minutes</button>}
      {item?.state === "approved" && <button type="button" disabled={busy || !confirmed} onClick={() => void run("execute")}>Update the {k.noun} once</button>}
      {(item?.state === "executing" || item?.state === "unknown" || item?.state === "verified") && <button type="button" disabled={busy} onClick={() => void run("verify")}>Read back from QuickBooks</button>}
    </>}
    {message && <p role="status">{message}</p>}
  </details>;
}
