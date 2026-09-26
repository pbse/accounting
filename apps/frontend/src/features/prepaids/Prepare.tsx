import { PrepaidMethodFields, readCredits, scheduleTerms, type Schedule } from "./MethodFields";
import { useRevision } from "../closes/useRevision";
import { SourceReview } from "../closes/Evidence";
import { useEffect, useRef, useState, type FormEvent } from "react";
import type {
  PrepaidInput,
  PrepaidWorkpaper,
} from "../../../../../packages/contracts/prepaid";
import { useSearchParams } from "react-router";
import { SaveDraft } from "../closes/SaveDraft";
import { money, useCurrency } from "../closes/workspace";
import { authenticationHeaders } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";

function download(contents: string, name: string, type: string) {
  const url = URL.createObjectURL(new Blob([contents], { type }));
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

export default function Prepare() {
  const [params] = useSearchParams();
  const currency = useCurrency(params.get("close"));
  const schedule = (params.get("schedule") ?? "") as Schedule;
  const terms = scheduleTerms[schedule] ?? scheduleTerms[""];
  const form = useRef<HTMLFormElement>(null);
  const revision = useRevision(form);
  const resultHeading = useRef<HTMLHeadingElement>(null);
  const [result, setResult] = useState<PrepaidWorkpaper>();
  const [error, setError] = useState<{ message: string; field: string }>();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (result) resultHeading.current?.focus();
    if (error && !busy) {
      const field = form.current?.elements.namedItem(error.field);
      if (field instanceof HTMLElement) field.focus();
    }
  }, [result, error, busy]);

  function loadExample(mode="full") {
    const example = {
      entity: params.get("entity") ?? "Acme Services (example)",
      source_reference: "Example invoice 001, page 1",
      amount: "12000.00",
      method: "equal_full_months_v1",
      booked_balance:"", booking_reference:"", credit_amount:"", credit_period:"", credit_reference:"", cancellation_method:"", cancellation_period:"", cancellation_reference:"",
      service_start: "2026-09-01",
      service_end: "2027-08-31",
      close_period: params.get("period") ?? "2026-09",
      expense_account: "Software expense",
      prepaid_account: "Prepaid expense",
    };
    if(mode!=="full") Object.assign(example,{amount:"100.00",method:"actual_days_v1",source_reference:"Synthetic actual-day example v1",service_start:"2028-01-16",service_end:"2028-03-15",close_period:"2028-02"});
    if(mode==="credit") Object.assign(example,{credit_amount:"10.00",credit_period:"2028-02",credit_reference:"Synthetic credit example v1",booked_balance:"30.00",booking_reference:"Synthetic ledger after credit"});
    if(mode==="refund" || mode==="writeoff") Object.assign(example,{method:"equal_full_months_v1",service_start:"2028-01-01",service_end:"2028-03-31",close_period:"2028-02",cancellation_method:mode==="refund"?"full_refund_month_boundary_v1":"no_refund_month_boundary_v1",cancellation_period:"2028-02",cancellation_reference:"Synthetic cancellation evidence",booked_balance:mode==="refund"?"0.00":"66.67",booking_reference:"Synthetic February ledger"});
    for (const [name, value] of Object.entries(example)) {
      (form.current!.elements.namedItem(name) as HTMLInputElement).value =
        value;
    }
    for (let i=0;i<20;i++) for (const key of ["amount","effective","recorded","reference"]) { const field=form.current?.elements.namedItem(`credit_${key}_${i}`); if(field instanceof HTMLInputElement) field.value=""; }
    for (const name of ["method_confirmed", "booking_confirmed", "examples_confirmed"]) {
      (form.current!.elements.namedItem(name) as HTMLInputElement).checked =
        false;
    }
    setResult(undefined);
    setError(undefined);
  }

  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const text = (name: string) => String(data.get(name) ?? "");
    const input: PrepaidInput = {
      entity: text("entity"),
      source_reference: text("source_reference"),
      amount: text("amount"),
      service_start: text("service_start"),
      service_end: text("service_end"),
      close_period: text("close_period"),
      expense_account: text("expense_account"),
      prepaid_account: text("prepaid_account"),
      currency,
      ...(schedule && { schedule }),
      ...(text("salvage_value") && { salvage_value: text("salvage_value") }),
      ...(text("factor") && { factor: text("factor") as PrepaidInput["factor"] }),
      ...(text("convention") && { convention: "mid_month" as const }),
      ...((["disposal_period", "disposal_proceeds", "disposal_reference", "asset_account", "proceeds_account", "gain_loss_account"].some(text) || data.has("disposal_confirmed")) && { disposal_period: text("disposal_period"), disposal_proceeds: text("disposal_proceeds"), disposal_reference: text("disposal_reference"), asset_account: text("asset_account"), proceeds_account: text("proceeds_account"), gain_loss_account: text("gain_loss_account"), disposal_confirmed: data.has("disposal_confirmed") }),
      basis: "accrual",
      method: text("method") as PrepaidInput["method"],
      examples_confirmed: data.has("examples_confirmed"),
      booked_balance:text("booked_balance"), booking_reference:text("booking_reference"),
      credit_amount:text("credit_amount"),credit_period:text("credit_period"),credit_reference:text("credit_reference"),
      credits: readCredits(data), accountant_rule_reference: text("accountant_rule_reference"),
      cancellation_method:text("cancellation_method") as PrepaidInput["cancellation_method"],cancellation_period:text("cancellation_period"),cancellation_reference:text("cancellation_reference"),
      method_confirmed: data.get("method_confirmed") === "on",
      booking_confirmed: data.get("booking_confirmed") === "on",
    };
    setBusy(true);
    setError(undefined);
    setResult(undefined);
    try {
      const response = await fetch(`${apiBaseUrl}/v1/workpapers:prepare`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(await authenticationHeaders()),
        },
        body: JSON.stringify(input),
      });
      if (!response.ok) {
        const problem = await response.json();
        setError({
          message:
            problem.message ??
            "The workpaper could not be prepared. Please try again.",
          field: problem.field ?? "",
        });
        return;
      }
      setResult(await response.json());
    } catch {
      setError({
        message:
          "Preparation is unavailable. Your inputs are still here; check your connection and try again.",
        field: "",
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <p className="eyebrow">{terms.name.toUpperCase()} / DRAFT PREPARATION</p>
      <h1>From invoice to review-ready calculation.</h1>
      <p className="intro">
        Enter the source terms and confirm the method. We’ll calculate the
        schedule and a proposed adjustment. Save the draft to a client close
        when ready; nothing is posted.
      </p>
      <details className="preparation-inputs" open={!result}>
        <summary>
          {result
            ? "Edit source terms and prepare again"
            : "Source terms and accounting assumptions"}
        </summary>
        {revision.paper && (
          <section>
            <h2>Revise draft #{revision.paper}</h2>
            <p>
              Prior values are prefilled. Check all terms and balances against
              current evidence and confirm the method again. Saving linked
              instructions automatically queues a fresh draft; prior
              calculations and reviews remain in history.
            </p>
            {!revision.ready && (
              <p role="status">
                {revision.error || "Loading prior work and current evidence…"}
              </p>
            )}
            {revision.close && (
              <SourceReview
                closeId={revision.close}
                sources={revision.sources}
              />
            )}
          </section>
        )}
        <form
          ref={form}
          onSubmit={prepare}
          onChange={() => {
            setResult(undefined);
            setError(undefined);
          }}
        >
          <fieldset disabled={busy || !revision.ready}>
            <legend>1. Source terms</legend>
            <p className="source-label">
              Use the invoice and your ledger to supply these facts. Source
              references are entered by you; Close has not verified the document
              or ledger.
            </p>
            {schedule === "" && <>
            <button
              type="button"
              className="example-button"
              onClick={()=>loadExample()}
            >
              Use the $12,000 example
            </button>
            <button type="button" onClick={()=>loadExample("daily")}>Use synthetic actual-day example</button>
            <button type="button" onClick={()=>loadExample("credit")}>Use synthetic credit and prior-balance example</button>
            <button type="button" onClick={()=>loadExample("refund")}>Use synthetic full-refund cancellation</button>
            <button type="button" onClick={()=>loadExample("writeoff")}>Use synthetic no-refund cancellation</button>
            </>}
            <div className="form-grid">
              <label>
                Client / entity
                <input
                  name="entity"
                  defaultValue={params.get("entity") ?? ""}
                  required
                  maxLength={200}
                  autoComplete="off"
                />
              </label>
              <label>
                {terms.source}
                <input
                  name="source_reference"
                  required
                  maxLength={200}
                  placeholder="Invoice number, document and page"
                  autoComplete="off"
                />
              </label>
              <label>
                {terms.amount} ({currency})
                <input
                  name="amount"
                  required
                  inputMode="decimal"
                  maxLength={13}
                  placeholder="12000.00"
                />
              </label>
              <label>
                Close month
                <input
                  name="close_period"
                  type="month"
                  required
                  defaultValue={params.get("period") ?? ""}
                />
              </label>
              <label>
                {terms.start}
                <input name="service_start" type="date" required />
                <small>Inclusive start; equal-month method requires the first day.</small>
              </label>
              <label>
                {terms.end}
                <input name="service_end" type="date" required />
                <small>Inclusive end; up to 120 calendar months.</small>
              </label>
              <label>
                {terms.expense}
                <input
                  name="expense_account"
                  required
                  maxLength={200}
                  placeholder="Software expense"
                />
              </label>
              <label>
                {terms.balance}
                <input
                  name="prepaid_account"
                  required
                  maxLength={200}
                  placeholder="Prepaid expense"
                />
              </label>
            </div>
          </fieldset>
          <fieldset disabled={busy || !revision.ready}>
            <legend>2. Confirm this method applies</legend>
            <PrepaidMethodFields key={revision.savedInput ? "revision-loaded" : "new"} values={revision.savedInput as Record<string,string> | undefined} credits={revision.savedInput?.credits as PrepaidInput["credits"] | undefined} schedule={schedule} />
          </fieldset>
          {error && (
            <p className="form-error" role="alert">
              {error.message}
            </p>
          )}
          <button
            className="primary"
            disabled={busy || !revision.ready}
            type="submit"
          >
            {busy ? "Preparing schedule…" : "Prepare workpaper →"}
          </button>
          {busy && (
            <p role="status">
              Calculating the schedule from your confirmed inputs.
            </p>
          )}
        </form>
      </details>
      {result && (
        <div className="prepared-result">
          <p className="eyebrow">
            PREPARED DRAFT / {result.input.close_period}
          </p>
          <h2 ref={resultHeading} tabIndex={-1}>
            {result.input.entity} · {terms.name.toLowerCase()} workpaper
          </h2>
          <p className="note">
            Export or save this draft before leaving or refreshing. Editing the
            terms clears this calculation until you prepare again.
          </p>
          <div className="review-grid">
            <section>
              <h3>Source and method</h3>
              <dl>
                <dt>Source reference</dt>
                <dd>{result.input.source_reference}</dd>
                <dt>Source status</dt>
                <dd>User-supplied; not independently verified.</dd>
                <dt>Invoice amount</dt>
                <dd>{money(result.input.amount, result.input.currency)}</dd>
                <dt>Service dates</dt>
                <dd>
                  {result.input.service_start} → {result.input.service_end}
                </dd>
                <dt>Method</dt>
                <dd>{result.input.method}{result.input.factor && ` × ${result.input.factor}`}{result.input.convention && ` · ${result.input.convention}`}{result.input.accountant_rule_reference && ` · ${result.input.accountant_rule_reference}`} · residual in final month.</dd>
                {result.input.cancellation_method && <><dt>Cancellation</dt><dd>{result.input.cancellation_method} effective {result.input.cancellation_period} · {result.input.cancellation_reference}</dd></>}
                {!!result.input.credits?.length && <><dt>Reviewed credit rule</dt><dd>{result.input.accountant_rule_reference}</dd><dt>Credits</dt><dd>{result.input.credits.map(c => `${money(c.amount, result.input.currency)} effective ${c.effective_period}, booked ${c.recorded_period} · ${c.reference}`).join("; ")}</dd></>}
                {result.input.salvage_value && <><dt>Salvage value</dt><dd>{money(result.input.salvage_value, result.input.currency)}</dd></>}
                <dt>Booking assumption</dt>
                <dd>{result.input.booked_balance ? `${terms.booked}: ${money(result.input.booked_balance, result.input.currency)} · ${result.input.booking_reference}` : terms.blank}</dd>
              </dl>
            </section>
            <section>
              <h3>Calculated at month-end</h3>
              <dl>
                <dt>{terms.recognized} this month</dt>
                <dd>{money(result.period_expense, result.input.currency)}</dd>
                <dt>{terms.recognized} through this month</dt>
                <dd>{money(result.recognized_expense, result.input.currency)}</dd>
                <dt>{terms.remaining}</dt>
                <dd>{money(result.prepaid_balance, result.input.currency)}</dd>
              </dl>
              <p className="source-label">
                {result.input.currency} amounts calculated in exact cents. This preview assumes the
                stated initial booking; no ledger balance has been read.
              </p>
            </section>
          </div>
          <section>
            <h3>Proposed reclassification</h3>
            {result.proposal ? (
              <table>
                <caption>
                  {result.proposal.date} · draft only, not approved or posted
                </caption>
                <thead>
                  <tr>
                    <th scope="col">Account</th>
                    <th scope="col">Debit ({result.input.currency})</th>
                    <th scope="col">Credit ({result.input.currency})</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <th scope="row">{result.proposal.debit_account}</th>
                    <td>{result.proposal.amount}</td>
                    <td>—</td>
                  </tr>
                  <tr>
                    <th scope="row">{result.proposal.credit_account}</th>
                    <td>—</td>
                    <td>{result.proposal.amount}</td>
                  </tr>
                </tbody>
              </table>
            ) : (
              <p>
                The scheduled balance matches the confirmed booked balance. No adjustment is proposed.
              </p>
            )}
            {result.disposal && <ul aria-label="Disposal entries">{result.disposal.map((e, i) => <li key={i}>Disposal {e.date}, book by hand: debit {e.debit_account}, credit {e.credit_account}, {e.amount}</li>)}</ul>}
          </section>
          <section>
            <h3>Full schedule · {result.schedule.length} months</h3>
            <table>
              <caption>
                {result.input.method} · credits and full refunds reduce prepaid in their effective month
              </caption>
              <thead>
                <tr>
                  <th scope="col">Period</th>
                  <th scope="col">{terms.recognized} ({result.input.currency})</th>
                  <th scope="col">Remaining ({result.input.currency})</th><th scope="col">Credit ({result.input.currency})</th>
                </tr>
              </thead>
              <tbody>
                {result.schedule.map((row) => (
                  <tr key={row.period}>
                    <th scope="row">{row.period}</th>
                    <td>{row.expense}</td>
                    <td>{row.remaining}</td><td>{row.credit ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
          <SaveDraft
            kind="prepaid"
            input={result.input}
            initialSources={Object.fromEntries(
              revision.sources.map((s) => [s.role, s]),
            )}
          />
          <div className="export-actions">
            <button
              onClick={() =>
                download(
                  JSON.stringify(result, null, 2),
                  "prepaid-workpaper.json",
                  "application/json",
                )
              }
            >
              Export workpaper JSON
            </button>
            <button
              onClick={() =>
                download(
                  [
                    `Period,Expense ${result.input.currency},Remaining ${result.input.currency},Credit or refund ${result.input.currency}`,
                    ...result.schedule.map(
                      (row) => `${row.period},${row.expense},${row.remaining},${row.credit ?? "0.00"}`,
                    ),
                  ].join("\r\n"),
                  "prepaid-schedule.csv",
                  "text/csv;charset=utf-8",
                )
              }
            >
              Export schedule CSV
            </button>
          </div>
        </div>
      )}
    </>
  );
}
