import { useState, type FormEvent } from "react";
import type { PrepaidWorkpaper } from "../../../../../packages/contracts/prepaid";
import type { Source } from "./Evidence";
import { workspaceRequest } from "./workspace";

type Intent = { state: string; digest?: string; realm?: string; vendor_name?: string; unbooked_confirmed_by?: string; credit?: unknown; readback?: unknown; error?: string; external_id?: string; approval_expires_at?: string; configured?: boolean };

export function VendorCredit({ closeId, paperId, paper, sources, approved }: { closeId: string; paperId: string; paper: PrepaidWorkpaper; sources: Source[]; approved: boolean }) {
  const [intent, setIntent] = useState<Intent>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const path = `/${closeId}/workpapers/${paperId}/vendor-credit`;
  const credits = paper.input.credits?.length ? paper.input.credits.filter(c => c.recorded_period === paper.input.close_period) : paper.input.credit_amount && paper.input.credit_period === paper.input.close_period ? [{ amount: paper.input.credit_amount, reference: paper.input.credit_reference ?? "" }] : [];
  if (!credits.length) return null;
  async function run(input?: object) {
    setBusy(true); setError("");
    try { setIntent(await workspaceRequest(path, input)); setConfirmation(""); }
    catch (e) { setError(e instanceof Error ? e.message : "Vendor credit outcome unavailable; refresh before acting."); }
    finally { setBusy(false); }
  }
  function preview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    void run({ action: "preview", vendor_id: String(data.get("vendor_id")), ap_account_id: String(data.get("ap_account_id")), evidence_id: String(data.get("evidence_id")), credit_reference: String(data.get("credit_reference")), txn_date: String(data.get("txn_date")), method_confirmed: data.has("method_confirmed"), unbooked_confirmed: data.has("unbooked_confirmed") });
  }
  return <details><summary>Reviewed supplier credit · QuickBooks sandbox</summary>
    <p>This creates a native VendorCredit for one reviewed supplier credit. It credits the mapped prepaid asset and the exact A/P account. The supplier credit document must be linked to this approved workpaper. This sandbox flow does not authorize production posting.</p>
    <button disabled={busy} onClick={() => void run()}>Check credit status</button>
    {error && <p role="alert">{error}</p>}
    {intent && <><p role="status">State: {intent.state}{intent.error ? ` · ${intent.error}` : ""}</p>
      {intent.realm && <p>Sandbox company #{intent.realm}</p>}
      {intent.vendor_name && <p>QuickBooks vendor: {intent.vendor_name}. Confirm this is the supplier named on the linked credit before approval.</p>}
      {intent.unbooked_confirmed_by && <p>Unbooked-credit check confirmed by {intent.unbooked_confirmed_by} for this preview. Recheck the books immediately before posting.</p>}
      {intent.external_id && <p>QuickBooks VendorCredit #{intent.external_id}</p>}
      {intent.credit && <details open><summary>Exact approved provider payload</summary><pre>{JSON.stringify(intent.credit, null, 2)}</pre></details>}
      {intent.readback && <details><summary>Canonical QuickBooks readback</summary><pre>{JSON.stringify(intent.readback, null, 2)}</pre></details>}
      {["not_prepared", "preview", "approved"].includes(intent.state) && approved && intent.configured !== false && <form onSubmit={preview}>
        <label>Reviewed supplier credit<select name="credit_reference" required defaultValue=""><option value="">Select credit</option>{credits.map(c => <option key={c.reference} value={c.reference}>{c.reference} · ${c.amount}</option>)}</select></label>
        <label>Linked supplier credit document<select name="evidence_id" required defaultValue=""><option value="">Select source</option>{sources.map(s => <option key={s.id} value={s.id}>{s.name} · #{s.id}</option>)}</select></label>
        <label>Exact QuickBooks vendor ID<input name="vendor_id" required inputMode="numeric" pattern="[0-9]{1,30}" /></label>
        <label>Exact QuickBooks Accounts Payable ID<input name="ap_account_id" required inputMode="numeric" pattern="[0-9]{1,30}" /></label>
        <label>Supplier credit date in this close month<input name="txn_date" required type="date" min={`${paper.input.close_period}-01`} max={`${paper.input.close_period}-31`} /></label>
        <p>Synthetic example: a $25 supplier credit against an unpaid bill reduces Accounts Payable and the prepaid asset by $25. A cash refund or a credit against a different account needs a separate treatment.</p>
        <label><input type="checkbox" name="method_confirmed" required /> The client accountant approved this A/P treatment, checked the supplier document and synthetic example, and confirmed it is not a cash refund.</label>
        <label><input type="checkbox" name="unbooked_confirmed" required /> I checked the current QuickBooks books and confirmed this credit has not been recorded or included in the booked prepaid balance. Posting it will require a fresh ledger capture and workpaper review.</label>
        <button className="primary" disabled={busy}>{intent.state === "not_prepared" ? "Preview native VendorCredit" : "Replace preview and reset approval"}</button>
      </form>}
      {(intent.state === "preview" || intent.state === "approved") && <><p>Accountant approval binds this exact vendor, evidence, date, amount and accounts for ten minutes. A posting attempt is never retried automatically.</p><label>Type {intent.state === "preview" ? "APPROVE VENDOR CREDIT" : "POST VENDOR CREDIT"}<input value={confirmation} onChange={e => setConfirmation(e.target.value)} autoComplete="off" /></label><button disabled={busy || confirmation !== (intent.state === "preview" ? "APPROVE VENDOR CREDIT" : "POST VENDOR CREDIT")} onClick={() => void run({ action: intent.state === "preview" ? "approve" : "execute", digest: intent.digest, confirmation })}>{intent.state === "preview" ? "Approve exact credit" : "Post once to sandbox"}</button>{intent.approval_expires_at && <p>Approval expires {new Date(intent.approval_expires_at).toLocaleString()}.</p>}</>}
      {intent.external_id && <button disabled={busy} onClick={() => void run({ action: "verify", digest: intent.digest })}>Verify saved VendorCredit in QuickBooks</button>}
    </>}
  </details>;
}
