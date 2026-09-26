import { useState } from "react";
import type { PrepaidInput } from "../../../../../packages/contracts/prepaid";

export function readCredits(data: FormData): NonNullable<PrepaidInput["credits"]> {
  return Array.from({ length: 20 }, (_, i) => ({
    amount: String(data.get(`credit_amount_${i}`) ?? "").trim(),
    effective_period: String(data.get(`credit_effective_${i}`) ?? "").trim(),
    recorded_period: String(data.get(`credit_recorded_${i}`) ?? "").trim(),
    reference: String(data.get(`credit_reference_${i}`) ?? "").trim(),
  })).filter(c => Object.values(c).some(Boolean));
}

export type Schedule = "" | NonNullable<PrepaidInput["schedule"]>;
// Deferred revenue and depreciation reuse the straight-line prepaid schedule under their own names.
export const scheduleTerms: Record<Schedule, Record<"name" | "source" | "amount" | "start" | "end" | "expense" | "balance" | "booked" | "blank" | "recognized" | "remaining", string>> = {
  "": { name: "Prepaid", source: "Invoice reference and source location", amount: "Invoice amount", start: "Service starts", end: "Service ends", expense: "Expense account", balance: "Prepaid account", booked: "Booked prepaid balance", blank: "Fully expensed; no prior adjustments or credits.", recognized: "Expense", remaining: "Remaining prepaid" },
  deferred_revenue: { name: "Deferred revenue", source: "Customer invoice or contract reference", amount: "Amount billed in advance", start: "Service starts", end: "Service ends", expense: "Revenue account", balance: "Deferred revenue account", booked: "Booked deferred revenue balance", blank: "Booked straight to revenue; no prior adjustments or credits.", recognized: "Revenue", remaining: "Remaining deferred revenue" },
  depreciation: { name: "Depreciation", source: "Asset and purchase invoice reference", amount: "Asset cost", start: "Depreciation starts (first day of the in-service month for equal months)", end: "Last day of the useful life", expense: "Depreciation expense account", balance: "Accumulated depreciation account", booked: "Booked accumulated depreciation", blank: "", recognized: "Depreciation", remaining: "Remaining to depreciate" },
};

export function PrepaidMethodFields({values={},credits=[],schedule=""}: {values?: Record<string,string>;credits?: NonNullable<PrepaidInput["credits"]>;schedule?: Schedule}) {
  const [creditRows, setCreditRows] = useState(Array.from({length:Math.max(1,credits.length)},(_,i)=>i));
  return <>
    <label>Allocation method<select name="method" required defaultValue="" onChange={e=>{
      for(const name of ["method_confirmed","examples_confirmed"]){const field=e.currentTarget.form?.elements.namedItem(name);if(field instanceof HTMLInputElement)field.checked=false;}
    }}><option value="">Choose the accountant-approved method</option><option value="equal_full_months_v1">Equal full calendar months · v1</option><option value="actual_days_v1">Actual service days, inclusive · v1</option>{schedule === "depreciation" && <option value="declining_balance_v1">Declining balance · v1</option>}</select></label>
    <details><summary>Synthetic examples to review before confirming</summary>
      <p>These illustrate supported calculations, not a recommendation of accounting policy. Accrual basis only, in the client's home currency. Each month rounds down to cents; the last month receives the residual.</p>
      <table><caption>Method examples · version 1</caption><thead><tr><th>Terms</th><th>Expected result</th></tr></thead><tbody>
        <tr><td>$100, September–November 2026, equal full months</td><td>$33.33 / $33.33 / $33.34</td></tr>
        <tr><td>$100, January 16–March 15, 2028, actual days (16 / 29 / 15 days)</td><td>$26.66 / $48.33 / $25.01</td></tr>
        <tr><td>Same daily schedule, $10 credit effective February</td><td>January stays $26.66; February $41.74; March $21.60. Total expense $90.</td></tr>
        <tr><td>February target $21.60; ledger prepaid $30 after all entries</td><td>Debit expense / credit prepaid $8.40. If already $21.60, no adjustment.</td></tr>
        <tr><td>$100 January–March 2028 equal full months, cancel February with full refund</td><td>January expense $33.33; February refund $66.67; remaining prepaid zero. Confirm the refund was booked before preparing.</td></tr>
        <tr><td>Same cancellation, no refund</td><td>January expense $33.33; February write-off expense $66.67; remaining prepaid zero. Confirm the booked balance before the proposed write-off.</td></tr>
        <tr><td>$120 January–March, $10 effective and recorded February, $5 effective January and recorded March</td><td>Revised schedule: January $38.33, February $33.33, March $33.34; total expense $105. Current close true-up compares the revised target with the booked prepaid balance; prior-period journals are not silently changed.</td></tr>
        <tr><td>Deferred revenue: 12,000 billed for September 2026–August 2027; booked liability 12,000</td><td>September earns 1,000: debit deferred revenue / credit revenue 1,000.</td></tr>
        <tr><td>Depreciation: 3,600 laptop, September 2026–August 2029, equal months, salvage 360, booked accumulated depreciation 0</td><td>90.00 a month: debit depreciation expense / credit accumulated depreciation 90.00.</td></tr>
        <tr><td>Same laptop, no salvage, double declining balance (factor 2)</td><td>1/18 of book value a month: 200.00, then 188.88, 178.39…; from month 19 straight-line over the remaining months, 71.48 a month, with the 71.49 residual last. Never below salvage.</td></tr>
        <tr><td>Same laptop, equal months, mid-month convention</td><td>September 2026 50.00, then 100.00 a month, and 50.00 in September 2029, the month after the life ends.</td></tr>
      </tbody></table>
    </details>
    {schedule === "depreciation" ? <>
      <label>Booked accumulated depreciation<input name="booked_balance" required defaultValue={values.booked_balance ?? ""} inputMode="decimal" maxLength={13} placeholder="Enter 0.00 explicitly if none" /></label>
      <label>Ledger balance and cutoff reference<input name="booking_reference" required defaultValue={values.booking_reference ?? ""} maxLength={200} /></label>
      <label>Salvage value (optional)<input name="salvage_value" defaultValue={values.salvage_value ?? ""} inputMode="decimal" maxLength={13} /></label>
      <label>Declining-balance factor<select name="factor" defaultValue={values.factor ?? ""}><option value="">Not declining balance</option><option value="2">2 · double declining</option><option value="1.5">1.5</option></select></label>
      <label>Convention<select name="convention" defaultValue={values.convention ?? ""}><option value="">Full month (start on the first of the in-service month)</option><option value="mid_month">Mid-month: half a month at each end</option></select></label>
      <label>Client accountant's depreciation policy (required for declining balance or mid-month)<input name="accountant_rule_reference" maxLength={200} defaultValue={values.accountant_rule_reference ?? ""} placeholder="Policy and version" /></label>
      <label><input name="examples_confirmed" type="checkbox" required /> I reviewed these synthetic examples and the client's approved treatment. This confirms a draft method, not final workpaper or posting approval.</label>
    </> : <>
    <details><summary>Cancellation at a month boundary (optional)</summary>
      <p>Choose one reviewed treatment. Full refund assumes the remaining prepaid was refunded and booked; enter the balance after that entry (zero). No refund expenses the remaining prepaid in the cancellation month; enter the balance before any proposed write-off, or zero if already written off. Both require a ledger and cancellation source reference. Credits cannot be combined with a cancellation.</p>
      <label>Treatment<select name="cancellation_method" defaultValue={values.cancellation_method ?? ""}><option value="">No cancellation</option><option value="full_refund_month_boundary_v1">Full refund of the remaining balance</option><option value="no_refund_month_boundary_v1">No refund; recognize the remaining balance</option></select></label>
      <label>Effective cancellation month<input name="cancellation_period" type="month" defaultValue={values.cancellation_period ?? ""} /></label>
      <label>Cancellation/refund evidence and treatment reference<input name="cancellation_reference" maxLength={200} defaultValue={values.cancellation_reference ?? ""} /></label>
    </details>
    <label><input name="examples_confirmed" type="checkbox" required /> I reviewed these synthetic examples and the client's approved treatment. This confirms a draft method, not final workpaper or posting approval.</label>
    <details><summary>Credits and prior adjustments (optional)</summary>
      <p>Enter the current prepaid debit balance after all invoice, credit and adjusting entries through this close. The proposal is only the difference from the scheduled target. Blank means a fully expensed invoice with no credits or prior entries.</p>
      <label>{scheduleTerms[schedule].booked}<input name="booked_balance" defaultValue={values.booked_balance ?? ""} inputMode="decimal" maxLength={13} placeholder="Enter 0.00 explicitly if zero" /></label>
      <label>Ledger balance and cutoff reference<input name="booking_reference" defaultValue={values.booking_reference ?? ""} maxLength={200} /></label>
      <p>The single-credit fields below retain the original prospective method. For multiple or retrospective credits, use the separate reviewed rows. A retrospective credit revises the analytical schedule from its effective month; only the current-close difference from the confirmed booked balance is proposed. Prior-period journals are never changed automatically. Do not combine credit methods or cancellation.</p>
      <label>Credit amount<input name="credit_amount" defaultValue={values.credit_amount ?? ""} inputMode="decimal" maxLength={13} /></label>
      <label>Credit effective month<input name="credit_period" defaultValue={values.credit_period ?? ""} type="month" /></label>
      <label>Credit document and treatment reference<input name="credit_reference" defaultValue={values.credit_reference ?? ""} maxLength={200} /></label>
      <h4>Multiple or retrospective credits · separately reviewed method</h4>
      <label>Client accountant's approved rule reference<input name="accountant_rule_reference" maxLength={200} defaultValue={values.accountant_rule_reference ?? ""} placeholder="Approval record and version" /></label>
      {creditRows.map((row, index) => <fieldset key={row}><legend>Credit {index + 1}</legend>
        <label>Credit amount<input name={`credit_amount_${row}`} inputMode="decimal" maxLength={13} defaultValue={credits[row]?.amount ?? ""} /></label>
        <label>Effective service month<input name={`credit_effective_${row}`} type="month" defaultValue={credits[row]?.effective_period ?? ""} /></label>
        <label>Month booked in ledger<input name={`credit_recorded_${row}`} type="month" defaultValue={credits[row]?.recorded_period ?? ""} /></label>
        <label>Credit document and ledger reference<input name={`credit_reference_${row}`} maxLength={200} defaultValue={credits[row]?.reference ?? ""} /></label>
      </fieldset>)}
      <button type="button" disabled={creditRows.length >= 20} onClick={() => setCreditRows(rows => [...rows, Math.max(...rows) + 1])}>Add another credit</button>
    </details>
    </>}
    <label><input name="method_confirmed" type="checkbox" required /> Confirm the selected method, inclusive dates, each credit's effective and booked month, current-close true-up and final-month rounding under the client's approved rule.</label>
    <label><input name="booking_confirmed" type="checkbox" required /> Confirm accrual basis in the client's home currency and that the amount is recorded between the two stated accounts. The balance includes all credits and prior adjustments through this close; a blank balance means fully expensed with no credits or adjustments.</label>
  </>;
}
