import { useState, type FormEvent } from "react";
import { money as show, workspaceRequest } from "./workspace";
import { scheduleTerms, type Schedule } from "../prepaids/MethodFields";

// "prepaid balance", "deferred revenue balance" or "accumulated depreciation", as the ledger holds it.
const ledgerBalance = (schedule: Schedule = "") => scheduleTerms[schedule].booked.replace("Booked ", "");

type Item = {
  id: string;
  digest: string;
  scheduled_balance: string;
  schedule?: Schedule;
  account_id?: string;
  account_name?: string;
  location?: string;
  cells?: { value: string }[];
  columns?: string[];
  blocker?: string;
};
type Preview = {
  snapshot: { id?: string; period?: string; captured_at?: string };
  items: Item[];
};
const cents = (value: string) => {
  if (!/^(0|[1-9]\d*)(\.\d{1,2})?$/.test(value)) return null;
  const [whole, fraction = ""] = value.split(".");
  return BigInt(whole) * 100n + BigInt((fraction + "00").slice(0, 2));
};
const money = (value: bigint) => `${value / 100n}.${String(value % 100n).padStart(2, "0")}`;
const difference = (scheduled: string, entered: string, currency: string) => {
  const actual = cents(entered), expected = cents(scheduled);
  if (actual === null || expected === null) return "Enter a valid amount to compare.";
  return `Difference: ${show(money(actual > expected ? actual - expected : expected - actual), currency)} ${actual > expected ? "booked above schedule" : actual < expected ? "schedule above booked" : "matched"}.`;
};

export function Recurring({ closeId, currency, onPrepared }: { closeId: string; currency: string; onPrepared: () => void }) {
  const [preview, setPreview] = useState<Preview>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [queued, setQueued] = useState("");
  const [balances, setBalances] = useState<Record<string, string>>({});
  async function load() {
    setBusy(true); setError("");
    try { setPreview(await workspaceRequest(`/${closeId}/recurring`)); }
    catch (e) { setError(e instanceof Error ? e.message : "Could not compare recurring work."); }
    finally { setBusy(false); }
  }
  async function prepare(event: FormEvent<HTMLFormElement>, item: Item) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true); setError(""); setQueued("");
    try {
      const result = await workspaceRequest(`/${closeId}/recurring`, {
        paper_id: item.id, digest: item.digest, snapshot_id: preview?.snapshot.id,
        booked_balance: String(data.get("booked_balance") ?? ""), confirmed: data.has("confirmed"),
      });
      setQueued(`Preparation job #${result.job_id} queued for fresh review.`);
      await load();
      onPrepared();
    } catch (e) { setError(e instanceof Error ? e.message : "Could not prepare recurring work."); }
    finally { setBusy(false); }
  }
  return <details>
    <summary>Refresh recurring schedules</summary>
    <p>Capture this month’s QuickBooks period reports above, then compare carried prepaid, deferred revenue and depreciation schedules with each schedule's exact Trial Balance account. When a schedule's Trial Balance row is unambiguous and on its normal side, the agent prepares the comparison itself; its draft's ledger reference says the balance was read by the agent, and a person confirms the balance, method and entry when approving it. Otherwise enter the booked balance here. Nothing is posted without a person's separate posting approval.</p>
    <button disabled={busy} onClick={() => void load()}>Compare current period</button>
    {error && <p role="alert">{error}</p>}
    {queued && <p role="status">{queued}</p>}
    {preview && <>
      <p>{preview.snapshot.id ? `QuickBooks ${preview.snapshot.period} snapshot #${preview.snapshot.id}, captured ${new Date(preview.snapshot.captured_at!).toLocaleString()}` : "No current-period QuickBooks snapshot. Capture reports above first."}</p>
      {preview.items.length === 0 && <p>No carried schedules need a current-period balance comparison.</p>}
      {preview.items.map(item => <form key={item.id} onSubmit={e => void prepare(e, item)}>
        <fieldset disabled={busy || !!item.blocker}>
          <legend>Carried schedule #{item.id} · expected closing {ledgerBalance(item.schedule)} {show(item.scheduled_balance, currency)}</legend>
          {item.blocker ? <p role="status">{item.blocker}</p> : <>
            <p>QuickBooks {scheduleTerms[item.schedule ?? ""].balance.toLowerCase()}: {item.account_name} (#{item.account_id}). Trial Balance location: {item.location}.</p>
            <p>Reported cells: {item.cells?.map((c, i) => `${item.columns?.[i] || `Column ${i + 1}`}: ${c.value || "—"}`).join(" · ")}. Check the debit/credit signs, account mapping and report cutoff in the downloaded snapshot.</p>
            <label>Confirmed booked {ledgerBalance(item.schedule)} at this month’s cutoff ({currency})
              <input name="booked_balance" required inputMode="decimal" maxLength={13} pattern="(0|[1-9][0-9]*)(\.[0-9]{1,2})?" value={balances[item.id] ?? ""} onChange={e => setBalances(current => ({ ...current, [item.id]: e.target.value }))} />
            </label>
            <output>{difference(item.scheduled_balance, balances[item.id] ?? "", currency)}</output>
            <label><input name="confirmed" type="checkbox" required /> The client’s accounting professional confirmed this account, cutoff, booked balance, supported method and synthetic examples. Prepare a new draft for review; do not post it.</label>
            <button className="primary">Prepare reviewed comparison</button>
          </>}
        </fieldset>
      </form>)}
    </>}
  </details>;
}
