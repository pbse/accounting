import { useEffect, useMemo } from "react";
import type { ReferenceWorkpaper } from "../../../../../packages/contracts/reference";

export function Review({ workpaper }: { workpaper: ReferenceWorkpaper }) {
  // The example's JSON, as loaded, for download.
  const download = useMemo(() => URL.createObjectURL(new Blob([JSON.stringify(workpaper, null, 2)], { type: "application/json" })), [workpaper]);
  useEffect(() => () => URL.revokeObjectURL(download), [download]);
  return (
    <>
      <div className="review-grid">
        <section>
          <p className="eyebrow">01 / EVIDENCE & POLICY</p>
          <h2>Annual software subscription</h2>
          <p className="source-label">
            Synthetic source transcription · {workpaper.invoice.id} · page{" "}
            {workpaper.invoice.page}
          </p>
          <dl>
            <dt>Invoice amount</dt>
            <dd>${workpaper.invoice.amount}</dd>
            <dt>Service dates</dt>
            <dd>
              {workpaper.invoice.service_start} →{" "}
              {workpaper.invoice.service_end}
            </dd>
            <dt>Recorded in ledger</dt>
            <dd>
              ${workpaper.ledger.booked_amount} · {workpaper.ledger.account}
            </dd>
            <dt>Ledger reference</dt>
            <dd>
              {workpaper.ledger.id} · revision {workpaper.ledger.revision}
            </dd>
          </dl>
          <hr />
          <h3>Approved fixture assumption</h3>
          <p>{workpaper.policy.method}</p>
          <p className="source-label">
            {workpaper.policy.id} · version {workpaper.policy.version}. This is
            a test assumption, not an approved customer procedure.
          </p>
        </section>
        <section>
          <p className="eyebrow">02 / CALCULATION & PROPOSED EFFECT</p>
          <h2>$1,000.00 September expense</h2>
          <p>
            $12,000.00 ÷ 12 equal months. The remaining $11,000.00 is the
            proposed prepaid balance.
          </p>
          <table>
            <caption>Proposed adjustment · September 30, 2026</caption>
            <thead>
              <tr>
                <th scope="col">Account</th>
                <th scope="col">Debit</th>
                <th scope="col">Credit</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <th scope="row">{workpaper.proposal.debit_account}</th>
                <td>${workpaper.proposal.amount}</td>
                <td>—</td>
              </tr>
              <tr>
                <th scope="row">{workpaper.proposal.credit_account}</th>
                <td>—</td>
                <td>${workpaper.proposal.amount}</td>
              </tr>
            </tbody>
          </table>
          <p className="note">
            Proposal only. No approval, ledger posting, or sign-off has
            occurred.
          </p>
          <button disabled aria-describedby="approval-boundary">
            Approve proposal
          </button>
          <p id="approval-boundary" className="source-label">
            This is an illustrative workpaper. Approval is unavailable in the
            reference workspace.
          </p>
        </section>
      </div>
      <section>
        <details>
          <summary>Inspect the full 12-month calculation</summary>
          <table>
            <caption>Equal-month allocation · synthetic fixture</caption>
            <thead>
              <tr>
                <th scope="col">Period</th>
                <th scope="col">Expense (USD)</th>
                <th scope="col">Remaining (USD)</th>
              </tr>
            </thead>
            <tbody>
              {workpaper.schedule.map((row) => (
                <tr key={row.period}>
                  <th scope="row">{row.period}</th>
                  <td>{row.expense}</td>
                  <td>{row.remaining}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
        <a
          className="download"
          href={download}
          download="synthetic-prepaid.json"
        >
          Download reference JSON
        </a>
      </section>
    </>
  );
}
