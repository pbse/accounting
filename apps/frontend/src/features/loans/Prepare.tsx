import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { apiBaseUrl } from "../../lib/api";
import { authenticationHeaders } from "../auth/Auth";
import { SaveDraft } from "../closes/SaveDraft";
import { money, useCurrency } from "../closes/workspace";
import { SourceReview } from "../closes/Evidence";
import { useRevision } from "../closes/useRevision";
import type { LoanFee, LoanInput, LoanPayment, LoanRateChange, LoanWorkpaper } from "../../../../../packages/contracts/loan";

const dayCounts = { actual_360: "Actual/360", actual_365: "Actual/365 (fixed)", "30_360_bond": "30/360 bond basis (US, no February rule)", "30e_360": "30E/360 (Eurobond)" };

export function LoanResult({ result }: { result: LoanWorkpaper }) {
  const input = result.input;
  const rated = result.schedule.some((row) => row.rates?.length);
  return (
    <section>
      <h3>Loan schedule · {input.lender} · {input.close_period}</h3>
      <p>
        {input.annual_rate}%{input.rate_changes?.map((c) => `, ${c.annual_rate}% from ${c.effective} (${c.reference})`).join("")} · {dayCounts[input.day_count]} · note {input.agreement_reference} · statement {input.source_reference}. The lender's split of each payment is used as stated; the note-rate column only checks it.
      </p>
      {result.blockers.map((b) => <p role="alert" key={b}>{b}</p>)}
      <div className="table-scroll">
      <table>
        <thead>
          <tr><th>Payment</th><th>Principal before</th><th>Days</th><th>Principal</th><th>Lender interest</th><th>Note-rate interest</th><th>Difference</th>{rated && <th>Rates</th>}</tr>
        </thead>
        <tbody>
          {result.schedule.length === 0 && <tr><td colSpan={7}>No payments this month.</td></tr>}
          {result.schedule.map((row) => (
            <tr key={row.date}><td>{row.date}</td><td>{money(row.principal_before, input.currency)}</td><td>{row.days}</td><td>{money(row.principal, input.currency)}</td><td>{money(row.interest, input.currency)}</td><td>{money(row.expected_interest, input.currency)}</td><td>{row.difference === "0.00" ? "—" : `${money(row.difference, input.currency)}`}</td>{rated && <td>{row.rates?.join(" then ")}</td>}</tr>
          ))}
        </tbody>
      </table>
      </div>
      <p>Lender principal: {money(input.opening_principal, input.currency)} opening{result.fees && ` + ${money(result.fees, input.currency)} fees added`} → {money(input.closing_principal, input.currency)} closing. Books differ by {money(result.book_difference, input.currency)}.</p>
      {input.fees?.map((f) => <p key={f.date + f.reference}>Fee {f.date}: {f.description}, {money(f.amount, input.currency)} ({f.reference}).</p>)}
      {result.accrued_interest && <p>Accrued interest {result.accrued_interest.from} → {result.accrued_interest.to}: {result.accrued_interest.days} {result.accrued_interest.days === 1 ? "day" : "days"}, {money(result.accrued_interest.amount, input.currency)}.</p>}
      {result.questions.length > 0 && <><h4>Questions before approval</h4><ul>{result.questions.map((q) => <li key={q}>{q}</li>)}</ul></>}
      <h4>Proposed entries</h4>
      {result.proposals.length === 0 ? <p>No entry is proposed.</p> : <ul>{result.proposals.map((e, i) => <li key={i}>{e.date} · debit {e.debit_account}, credit {e.credit_account}, {money(e.amount, input.currency)}</li>)}</ul>}
      <p>Draft only. Nothing is posted; book the approved entries in QuickBooks.</p>
    </section>
  );
}

const blank: LoanPayment = { date: "", principal: "", interest: "" };
const blankRate: LoanRateChange = { effective: "", annual_rate: "", reference: "" };
const blankFee: LoanFee = { date: "", amount: "", description: "", reference: "" };

export default function PrepareLoan() {
  const [params] = useSearchParams();
  const currency = useCurrency(params.get("close"));
  const form = useRef<HTMLFormElement>(null);
  const revision = useRevision(form);
  const [payments, setPayments] = useState<LoanPayment[]>([{ ...blank }]);
  const [rateChanges, setRateChanges] = useState<LoanRateChange[]>([]);
  const [fees, setFees] = useState<LoanFee[]>([]);
  const [method, setMethod] = useState<LoanInput["method"]>("lender_statement_v1");
  const [accrue, setAccrue] = useState("");
  const [reclass, setReclass] = useState("");
  const [result, setResult] = useState<LoanWorkpaper>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const saved = revision.savedInput as Partial<LoanInput> | undefined;
    if (!saved) return;
    setPayments(saved.payments?.length ? saved.payments : [{ ...blank }]);
    setRateChanges(saved.rate_changes ?? []);
    setFees(saved.fees ?? []);
    if (saved.method) setMethod(saved.method);
    setAccrue(String(!!saved.accrue_interest));
    setReclass(String(!!saved.reclass_current_portion));
  }, [revision.savedInput]);
  function example() {
    const values: Record<string, string> = { entity: params.get("entity") ?? "Synthetic loan client", close_period: "2026-09", lender: "First Bank", source_reference: "Synthetic statement Sep 2026", agreement_reference: "Synthetic note §2", annual_rate: "6.000", day_count: "actual_360", opening_principal: "120000.00", interest_paid_through: "2026-08-15", closing_principal: "118000.00", book_balance: "100000.00", loan_account: "Loan payable", interest_account: "Interest expense", accrued_interest_account: "Accrued interest", booked_accrued_interest: "0.00", principal_due_next_12_months: "20000.00", schedule_reference: "Synthetic amortization schedule p2", current_portion_account: "Current portion of loan", booked_current_portion: "18000.00" };
    setPayments([{ date: "2026-09-15", principal: "1500.00", interest: "620.00" }, { date: "2026-09-29", principal: "500.00", interest: "280.00" }]);
    setAccrue("true");
    setReclass("true");
    setResult(undefined);
    // The accrual and reclass fields mount after this render.
    setTimeout(() => {
      for (const [name, value] of Object.entries(values)) {
        const field = form.current?.elements.namedItem(name);
        if (field instanceof HTMLInputElement || field instanceof HTMLSelectElement) field.value = value;
      }
    });
  }
  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setResult(undefined);
    const data = new FormData(event.currentTarget);
    const text = (name: string) => String(data.get(name) ?? "").trim();
    const optional = (names: string[], on: boolean) => (on ? Object.fromEntries(names.map((n) => [n, text(n)])) : {});
    const input = {
      ...Object.fromEntries(["entity", "close_period", "lender", "source_reference", "agreement_reference", "annual_rate", "day_count", "opening_principal", "interest_paid_through", "closing_principal", "book_balance", "loan_account", "interest_account"].map((n) => [n, text(n)])),
      payments: payments.filter((p) => p.date || p.principal || p.interest),
      ...(rateChanges.length ? { rate_changes: rateChanges } : {}),
      ...(fees.length ? { fees, fee_account: text("fee_account"), booked_fees: text("booked_fees") } : {}),
      accrue_interest: accrue === "true",
      ...optional(["accrued_interest_account", "booked_accrued_interest"], accrue === "true"),
      reclass_current_portion: reclass === "true",
      ...optional(["principal_due_next_12_months", "schedule_reference"], reclass === "true"),
      ...optional(["current_portion_account", "booked_current_portion"], reclass === "true" || !!text("current_portion_account")),
      currency,
      method,
      method_confirmed: data.has("method_confirmed"),
      rate_confirmed: data.has("rate_confirmed"),
      booking_confirmed: data.has("booking_confirmed"),
    } as LoanInput;
    try {
      const response = await fetch(`${apiBaseUrl}/v1/loans:prepare`, { method: "POST", headers: { "Content-Type": "application/json", ...(await authenticationHeaders()) }, body: JSON.stringify(input) });
      const value = await response.json();
      if (!response.ok) throw new Error(value.message ?? "Preparation unavailable.");
      setResult(value);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Preparation failed.");
    } finally {
      setBusy(false);
    }
  }
  const field = (name: string, label: string, type = "text", hint?: string, required = true) => (
    <label key={name}>
      {label}
      <input name={name} required={required} type={type} maxLength={200} inputMode={type === "text" && /principal|balance|rate|interest|due/.test(name) ? "decimal" : undefined} defaultValue={name === "entity" ? params.get("entity") ?? "" : name === "close_period" ? params.get("period") ?? "" : ""} />
      {hint && <small>{hint}</small>}
    </label>
  );
  const setPayment = (index: number, key: keyof LoanPayment, value: string) => setPayments((rows) => rows.map((row, i) => (i === index ? { ...row, [key]: value } : row)));
  const setRateChange = (index: number, key: keyof LoanRateChange, value: string) => setRateChanges((rows) => rows.map((row, i) => (i === index ? { ...row, [key]: value } : row)));
  const setFee = (index: number, key: keyof LoanFee, value: string) => setFees((rows) => rows.map((row, i) => (i === index ? { ...row, [key]: value } : row)));
  return (
    <>
      <h1>Prepare a loan schedule</h1>
      <p>Check a month of loan activity against the lender's statement. The lender's principal/interest split is used as stated; interest recomputed from the note only raises questions. Needs the firm's approved loan procedure for this client to save.</p>
      {revision.close && <SourceReview closeId={revision.close} sources={revision.sources} />}
      {revision.error && <p role="alert">{revision.error}</p>}
      <form ref={form} onSubmit={(e) => void prepare(e)} onChange={() => setResult(undefined)}>
        <fieldset disabled={busy || !revision.ready}>
          <legend>Loan terms from the note</legend>
          <button type="button" onClick={example}>Use synthetic loan example</button>
          <div className="form-grid">
            {field("entity", "Client / entity")}
            {field("close_period", "Close month", "month")}
            {field("lender", "Lender")}
            {field("agreement_reference", "Note or agreement reference")}
            {field("annual_rate", "Annual rate (%)", "text", "As stated in the note, for example 6.250")}
            <label>
              Day-count method
              <select name="day_count" required defaultValue="">
                <option value="">Choose the method stated in the note</option>
                {Object.entries(dayCounts).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
              </select>
            </label>
          </div>
        </fieldset>
        <fieldset disabled={busy || !revision.ready}>
          <legend>This month's lender statement</legend>
          <div className="form-grid">
            {field("source_reference", "Statement reference")}
            {field("interest_paid_through", "Interest paid through", "date", "Before this month's first payment")}
            {field("opening_principal", `Principal on that date (${currency})`)}
            {field("closing_principal", `Lender's closing principal (${currency})`)}
          </div>
          <table>
            <thead><tr><th>Payment date</th><th>Principal ({currency})</th><th>Interest ({currency})</th><th /></tr></thead>
            <tbody>
              {payments.map((payment, index) => (
                <tr key={index}>
                  <td><input aria-label={`Payment ${index + 1} date`} type="date" value={payment.date} onChange={(e) => setPayment(index, "date", e.target.value)} /></td>
                  <td><input aria-label={`Payment ${index + 1} principal`} inputMode="decimal" value={payment.principal} onChange={(e) => setPayment(index, "principal", e.target.value)} /></td>
                  <td><input aria-label={`Payment ${index + 1} interest`} inputMode="decimal" value={payment.interest} onChange={(e) => setPayment(index, "interest", e.target.value)} /></td>
                  <td><button type="button" onClick={() => setPayments((rows) => rows.filter((_, i) => i !== index))}>Remove</button></td>
                </tr>
              ))}
            </tbody>
          </table>
          <button type="button" disabled={payments.length >= 31} onClick={() => setPayments((rows) => [...rows, { ...blank }])}>Add payment</button>
          <h4>Rate changes and fees added to the balance</h4>
          <p>Only if the statement shows them. A new rate applies from its effective date; a fee bears interest from the day after it is added, after any payment that day. Fees paid in cash with a payment are already in the books and are not entered. Either needs the firm's approved variable-rate loan procedure to save.</p>
          {rateChanges.length > 0 && (
            <table>
              <thead><tr><th>Effective date</th><th>Annual rate (%)</th><th>Rate notice</th><th /></tr></thead>
              <tbody>
                {rateChanges.map((c, index) => (
                  <tr key={index}>
                    <td><input aria-label={`Rate change ${index + 1} date`} type="date" value={c.effective} onChange={(e) => setRateChange(index, "effective", e.target.value)} /></td>
                    <td><input aria-label={`Rate change ${index + 1} rate`} inputMode="decimal" value={c.annual_rate} onChange={(e) => setRateChange(index, "annual_rate", e.target.value)} /></td>
                    <td><input aria-label={`Rate change ${index + 1} notice`} maxLength={200} value={c.reference} onChange={(e) => setRateChange(index, "reference", e.target.value)} /></td>
                    <td><button type="button" onClick={() => setRateChanges((rows) => rows.filter((_, i) => i !== index))}>Remove</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <button type="button" disabled={rateChanges.length >= 12} onClick={() => { setRateChanges((rows) => [...rows, { ...blankRate }]); setMethod("lender_statement_variable_v1"); }}>Add rate change</button>
          {fees.length > 0 && (
            <table>
              <thead><tr><th>Date added</th><th>Amount ({currency})</th><th>Description</th><th>Statement line</th><th /></tr></thead>
              <tbody>
                {fees.map((f, index) => (
                  <tr key={index}>
                    <td><input aria-label={`Fee ${index + 1} date`} type="date" value={f.date} onChange={(e) => setFee(index, "date", e.target.value)} /></td>
                    <td><input aria-label={`Fee ${index + 1} amount`} inputMode="decimal" value={f.amount} onChange={(e) => setFee(index, "amount", e.target.value)} /></td>
                    <td><input aria-label={`Fee ${index + 1} description`} maxLength={200} value={f.description} onChange={(e) => setFee(index, "description", e.target.value)} /></td>
                    <td><input aria-label={`Fee ${index + 1} statement line`} maxLength={200} value={f.reference} onChange={(e) => setFee(index, "reference", e.target.value)} /></td>
                    <td><button type="button" onClick={() => setFees((rows) => rows.filter((_, i) => i !== index))}>Remove</button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <button type="button" disabled={fees.length >= 12} onClick={() => { setFees((rows) => [...rows, { ...blankFee }]); setMethod("lender_statement_variable_v1"); }}>Add fee</button>
        </fieldset>
        <fieldset disabled={busy || !revision.ready}>
          <legend>Ledger and month-end entries</legend>
          <div className="form-grid">
            {field("loan_account", "Loan account")}
            {field("book_balance", `Loan account balance at month-end (${currency})`)}
            {field("interest_account", "Interest expense account")}
            {fees.length > 0 && field("fee_account", "Fee expense account")}
            {fees.length > 0 && field("booked_fees", `Fees already booked to the loan this month (${currency}; enter zero explicitly)`)}
          </div>
          <label>
            Accrue interest to month-end?
            <select value={accrue} onChange={(e) => setAccrue(e.target.value)} required>
              <option value="">Choose per the client's procedure</option>
              <option value="true">Yes — accrual-basis client</option>
              <option value="false">No</option>
            </select>
          </label>
          {accrue === "true" && <div className="form-grid">{field("accrued_interest_account", "Accrued interest account")}{field("booked_accrued_interest", `Booked accrued interest (${currency}; enter zero explicitly)`)}</div>}
          <label>
            Reclassify the current portion this month?
            <select value={reclass} onChange={(e) => setReclass(e.target.value)} required>
              <option value="">Choose per the firm's cadence</option>
              <option value="true">Yes — this is a reclassification month</option>
              <option value="false">No</option>
            </select>
          </label>
          {reclass === "true" && <div className="form-grid">{field("principal_due_next_12_months", `Principal due in the next 12 months (${currency})`)}{field("schedule_reference", "Amortization schedule reference")}</div>}
          {reclass !== "" && <div className="form-grid">{field("current_portion_account", "Current portion account", "text", reclass === "true" ? undefined : "Only if part of the loan is kept in its own account", reclass === "true")}{field("booked_current_portion", `Booked current portion (${currency})`, "text", "Counts toward the loan balance", reclass === "true")}</div>}
          <label>
            Loan procedure approved for this client
            <select value={method} onChange={(e) => setMethod(e.target.value as LoanInput["method"])}>
              <option value="lender_statement_v1">Lender statement, fixed rate</option>
              <option value="lender_statement_variable_v1">Lender statement with rate changes and fees added to the balance</option>
            </select>
            <small>Must match the client's approved loan procedure to save. Rate changes and fees need the second one.</small>
          </label>
          <label><input name="method_confirmed" type="checkbox" required /> The lender's statement is the source of each payment's principal and interest; this schedule only checks it.</label>
          <label><input name="rate_confirmed" type="checkbox" required /> The rate and day-count method are from the note, any rate change is from the lender's notice, and opening principal is the balance on the interest-paid-through date.</label>
          <label><input name="booking_confirmed" type="checkbox" required /> Book balances are the ledger's month-end balances for these accounts.</label>
          <button className="primary">{busy ? "Checking…" : "Check loan schedule"}</button>
        </fieldset>
      </form>
      {error && <p role="alert">{error}</p>}
      {result && (
        <>
          <LoanResult result={result} />
          <SaveDraft kind="loan" input={result.input} initialSources={Object.fromEntries(revision.sources.map((s) => [s.role, s]))} />
        </>
      )}
    </>
  );
}
