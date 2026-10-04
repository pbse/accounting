import { useState, type FormEvent } from "react";
import { money, workspaceRequest } from "./workspace";

type Item = { id: string; digest: string; review_id: string; amount: string; rule: string; account_id?: string; account_name?: string; location?: string; cells?: { value: string }[]; blocker?: string; booked_balance?: string; reverse_next_month: boolean; suggested_evidence?: string };
type Evidence = { id: string; name: string; extraction_state: string; superseded_by: string | null };
type Preview = { snapshot: { id?: string; period?: string; captured_at?: string; source_id?: string }; items: Item[] };

export function RecurringAccruals({ closeId, currency, onPrepared }: { closeId: string; currency: string; onPrepared: () => void }) {
  const [preview, setPreview] = useState<Preview>();
  const [evidence, setEvidence] = useState<Evidence[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [queued, setQueued] = useState("");
  async function load() {
    // Fresh forms for every comparison, so a value or confirmation from an earlier capture never carries over.
    setBusy(true); setError(""); setPreview(undefined);
    try {
      const [next, sources] = await Promise.all([workspaceRequest(`/${closeId}/recurring-accruals`), workspaceRequest(`/${closeId}/evidence`)]);
      setPreview(next); setEvidence(sources);
    } catch (e) { setError(e instanceof Error ? e.message : "Could not compare recurring accruals."); }
    finally { setBusy(false); }
  }
  async function prepare(event: FormEvent<HTMLFormElement>, item: Item) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true); setError(""); setQueued("");
    try {
      const result = await workspaceRequest(`/${closeId}/recurring-accruals`, {
        paper_id: item.id, digest: item.digest, review_id: item.review_id,
        snapshot_id: preview?.snapshot.id, evidence_id: String(data.get("evidence_id")),
        booked_balance: String(data.get("booked_balance")), reverse_next_month: data.get("reverse_next_month") === "true",
        confirmed: data.has("confirmed"),
      });
      setQueued(`Accrual preparation job #${result.job_id} queued for a new accountant review.`);
      onPrepared(); await load();
    } catch (e) { setError(e instanceof Error ? e.message : "Could not queue the accrual."); }
    finally { setBusy(false); }
  }
  return <details><summary>Refresh recurring accruals</summary>
    <p>Start from an approved prior-month fixed recurring rule. Upload proof that this month’s service was incurred, capture current QuickBooks reports, and confirm the current booked liability. The prior approval never approves this month’s draft.</p>
    <button disabled={busy} onClick={() => void load()}>Compare recurring accruals</button>
    {error && <p role="alert">{error}</p>}{queued && <p role="status">{queued}</p>}
    {preview && <>
      <p>{preview.snapshot.id ? `Current ${preview.snapshot.period} QuickBooks snapshot #${preview.snapshot.id}` : "Capture current QuickBooks reports first."}</p>
      {preview.items.length === 0 && <p>No prior approved fixed recurring accruals were found.</p>}
      {preview.items.map(item => <form key={item.id} onSubmit={e => void prepare(e, item)}>
        <fieldset disabled={busy || !!item.blocker}>
          <legend>Prior reviewed accrual #{item.id} · monthly target {money(item.amount, currency)}</legend>
          <p>Client accountant rule: {item.rule}</p>
          {item.blocker ? <p role="status">{item.blocker}</p> : <>
            <p>Exact liability: {item.account_name} (#{item.account_id}). Trial Balance location: {item.location}. Report cells: {item.cells?.map(c => c.value || "—").join(" · ")}. Check cutoff and credit signs in the captured report.</p>
            <p className="note">Prefilled from the records: the booked balance is the account's credit balance in this capture's Trial Balance, the reversal is last month's choice, and the evidence is this month's document the agent read for the rule's amount, when there is exactly one. Check each before confirming.</p>
            <label>Current-month service evidence<select name="evidence_id" required defaultValue={item.suggested_evidence ?? ""}><option value="">Select uploaded proof</option>{evidence.filter(s => s.extraction_state === "ready" && !s.superseded_by && s.id !== preview.snapshot.source_id).map(s => <option key={s.id} value={s.id}>{s.name} · #{s.id}</option>)}</select></label>
            <label>Confirmed booked liability credit balance at this month’s cutoff ({currency})<input name="booked_balance" required inputMode="decimal" maxLength={13} pattern="(0|[1-9][0-9]*)(\.[0-9]{1,2})?" defaultValue={item.booked_balance ?? ""} /></label>
            <label>Propose reversal of this month’s adjustment next month?<select name="reverse_next_month" required defaultValue={String(item.reverse_next_month)}><option value="">Choose explicitly</option><option value="false">No</option><option value="true">Yes</option></select></label>
            <label><input type="checkbox" name="confirmed" required /> The client accountant confirmed the current service, exact ledger account, booked balance, recurring rule, synthetic example and reversal choice. Create a fresh draft only.</label>
            <button className="primary">Prepare this month’s accrual</button>
          </>}
        </fieldset>
      </form>)}
    </>}
  </details>;
}
