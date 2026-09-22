export function PrepaidMethodFields({values={}}: {values?: Record<string,string>}) {
  return <>
    <label>Allocation method<select name="method" required defaultValue="" onChange={e=>{
      for(const name of ["method_confirmed","examples_confirmed"]){const field=e.currentTarget.form?.elements.namedItem(name);if(field instanceof HTMLInputElement)field.checked=false;}
    }}><option value="">Choose the accountant-approved method</option><option value="equal_full_months_v1">Equal full calendar months · v1</option><option value="actual_days_v1">Actual service days, inclusive · v1</option></select></label>
    <details><summary>Synthetic examples to review before confirming</summary>
      <p>These illustrate supported calculations, not a recommendation of accounting policy. USD accrual basis only. Each month rounds down to cents; the last month receives the residual.</p>
      <table><caption>Method examples · version 1</caption><thead><tr><th>Terms</th><th>Expected result</th></tr></thead><tbody>
        <tr><td>$100, September–November 2026, equal full months</td><td>$33.33 / $33.33 / $33.34</td></tr>
        <tr><td>$100, January 16–March 15, 2028, actual days (16 / 29 / 15 days)</td><td>$26.66 / $48.33 / $25.01</td></tr>
        <tr><td>Same daily schedule, $10 credit effective February</td><td>January stays $26.66; February $41.74; March $21.60. Total expense $90.</td></tr>
        <tr><td>February target $21.60; ledger prepaid $30 after all entries</td><td>Debit expense / credit prepaid $8.40. If already $21.60, no adjustment.</td></tr>
      </tbody></table>
    </details>
    <label><input name="examples_confirmed" type="checkbox" required /> I reviewed these synthetic examples and the client's approved treatment. This confirms a draft method, not final workpaper or posting approval.</label>
    <details><summary>Credits and prior adjustments (optional)</summary>
      <p>Enter the current prepaid debit balance after all invoice, credit and adjusting entries through this close. The proposal is only the difference from the scheduled target. Blank means a fully expensed invoice with no credits or prior entries.</p>
      <label>Booked prepaid balance (USD)<input name="booked_balance" defaultValue={values.booked_balance ?? ""} inputMode="decimal" maxLength={13} placeholder="Enter 0.00 explicitly if zero" /></label>
      <label>Ledger balance and cutoff reference<input name="booking_reference" defaultValue={values.booking_reference ?? ""} maxLength={200} /></label>
      <p>One documented credit, applied prospectively at the start of the selected service month. No prior period is restated. Supply a booked balance after the credit entry. Multiple credits, mid-month cancellation and retrospective corrections need separate review.</p>
      <label>Credit amount (USD)<input name="credit_amount" defaultValue={values.credit_amount ?? ""} inputMode="decimal" maxLength={13} /></label>
      <label>Credit effective month<input name="credit_period" defaultValue={values.credit_period ?? ""} type="month" /></label>
      <label>Credit document and treatment reference<input name="credit_reference" defaultValue={values.credit_reference ?? ""} maxLength={200} /></label>
    </details>
    <label><input name="method_confirmed" type="checkbox" required /> Confirm the selected method, inclusive dates where applicable, prospective credit treatment and final-month rounding.</label>
    <label><input name="booking_confirmed" type="checkbox" required /> Confirm USD accrual basis and that net invoice cost is recorded between the stated expense and prepaid accounts. The balance includes all credits and prior adjustments through this close; a blank balance means fully expensed with no credits or adjustments.</label>
  </>;
}
