import { useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { apiBaseUrl } from "../../lib/api";
import { authenticationHeaders } from "../auth/Auth";
import { SaveDraft } from "../closes/SaveDraft";
import { money, useCurrency } from "../closes/workspace";
import { SourceReview } from "../closes/Evidence";
import { useRevision } from "../closes/useRevision";
import type { PayrollInput, PayrollWorkpaper } from "../../../../../packages/contracts/payroll";
import { PageHeader } from "@close/ui/components/PageHeader";

export function PayrollResult({ result }: { result: PayrollWorkpaper }) {
  const input = result.input;
  const show = (value: string) => money(value, input.currency);
  return (
    <section>
      <h3>Payroll · {input.provider} · {input.close_period}</h3>
      <p>
        {input.source_reference}, pay dates {input.first_pay_date} to {input.last_pay_date} · ledger {input.ledger_reference}
      </p>
      <p>
        Gross wages {show(input.gross_wages)} less employee taxes {show(input.employee_taxes)} and deductions {show(input.employee_deductions)} = net pay {show(input.net_pay)}. Employer
        taxes {show(input.employer_taxes)}, contributions {show(input.employer_contributions)}: total cost {show(result.total_cost)}. The provider debited{" "}
        {show(input.provider_debits)}; the ledger shows {show(input.ledger_debits)}. Still owed: {show(result.unpaid)}.
      </p>
      {result.blockers.map((b) => <p role="alert" key={b}>{b}</p>)}
      {result.proposals.length > 0 && (
        <ul>
          {result.proposals.map((p, i) => (
            <li key={i}>
              {p.date} · debit {p.debit_account}, credit {p.credit_account}, {show(p.amount)}
            </li>
          ))}
        </ul>
      )}
      <p>Draft only. This records the provider's summary and never runs payroll; the entry posts only after approval, under a posting grant.</p>
    </section>
  );
}

export default function PreparePayroll() {
  const [params] = useSearchParams();
  const currency = useCurrency(params.get("close"));
  const form = useRef<HTMLFormElement>(null);
  const revision = useRevision(form);
  const [result, setResult] = useState<PayrollWorkpaper>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setResult(undefined);
    const data = new FormData(event.currentTarget);
    const text = [...data].filter(([name]) => !name.endsWith("_confirmed")).map(([name, value]) => [name, String(value).trim()]).filter(([, value]) => value !== "");
    const input = { ...Object.fromEntries(text), currency, method: "payroll_summary_v1", approved_confirmed: data.has("approved_confirmed"), not_booked_confirmed: data.has("not_booked_confirmed") } as PayrollInput;
    try {
      const response = await fetch(`${apiBaseUrl}/v1/payroll:prepare`, { method: "POST", headers: { "Content-Type": "application/json", ...(await authenticationHeaders()) }, body: JSON.stringify(input) });
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
      <input name={name} required={required} type={type} maxLength={200} defaultValue={name === "entity" ? params.get("entity") ?? "" : name === "close_period" ? params.get("period") ?? "" : ""} />
      {hint && <small>{hint}</small>}
    </label>
  );
  const amount = (name: string, label: string, hint?: string) => field(name, `${label} (${currency})`, "text", hint);
  return (
    <>
      <PageHeader title="Payroll">
        Record a month's payroll from the provider's approved summary. The entry ties the summary's own figures and the provider's bank debits to the ledger, and leaves
        anything not yet paid as a liability. It never runs payroll.
      </PageHeader>
      {revision.close && <SourceReview closeId={revision.close} sources={revision.sources} />}
      {revision.error && <p role="alert">{revision.error}</p>}
      <form ref={form} onSubmit={(e) => void prepare(e)} onChange={() => setResult(undefined)}>
        <fieldset disabled={busy || !revision.ready}>
          <legend>The provider's summary</legend>
          <div className="form-grid">
            {field("entity", "Client / entity")}
            {field("close_period", "Close month", "month")}
            {field("provider", "Payroll provider", "text", "For example Gusto or ADP")}
            {field("source_reference", "Summary", "text", "The report's title and date range")}
            {field("first_pay_date", "First pay date", "date")}
            {field("last_pay_date", "Last pay date", "date")}
            {amount("gross_wages", "Gross wages")}
            {amount("employee_taxes", "Employee taxes withheld")}
            {amount("employee_deductions", "Other employee deductions", "Benefits, retirement and other withholdings")}
            {amount("net_pay", "Net pay")}
            {amount("employer_taxes", "Employer taxes")}
            {amount("employer_contributions", "Employer contributions", "Benefits or retirement match; 0.00 if none")}
            {amount("provider_debits", "Provider debits", "What the provider debited from the bank for these pay dates")}
          </div>
        </fieldset>
        <fieldset disabled={busy || !revision.ready}>
          <legend>The ledger</legend>
          <div className="form-grid">
            {field("ledger_reference", "Ledger evidence", "text", "The page of the account the payroll bank debits are booked to")}
            {amount("ledger_debits", "Payroll debits in the ledger")}
            {field("clearing_account", "Clearing account", "text", "Where the payroll bank debits are booked")}
            {field("wage_account", "Wage expense account")}
            {field("tax_expense_account", "Payroll tax expense account")}
            {field("benefits_account", "Benefits expense account", "text", "Needed only for employer contributions", false)}
            {field("liability_account", "Payroll liability account", "text", "For amounts withheld or owed but not yet paid")}
          </div>
          <label><input name="approved_confirmed" type="checkbox" required /> The summary is the provider's final, approved payroll for these pay dates, not a preview.</label>
          <label><input name="not_booked_confirmed" type="checkbox" required /> The ledger shows these bank debits in the clearing account, and this payroll is not yet recorded.</label>
        </fieldset>
        <button className="primary" disabled={busy || !revision.ready}>{busy ? "Checking…" : "Prepare payroll entry"}</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {result && (
        <>
          <PayrollResult result={result} />
          <SaveDraft kind="payroll" input={result.input} initialSources={Object.fromEntries(revision.sources.map((s) => [s.role, s]))} />
        </>
      )}
    </>
  );
}
