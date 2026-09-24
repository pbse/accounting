import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { apiBaseUrl } from "../../lib/api";
import { authenticationHeaders } from "../auth/Auth";
import { SaveDraft } from "../closes/SaveDraft";
import { SourceReview } from "../closes/Evidence";
import { useRevision } from "../closes/useRevision";
import type { EntityType, OwnerInput, OwnerWorkpaper } from "../../../../../packages/contracts/owner";

export const entityTypes: Record<EntityType, string> = { sole_proprietor: "Sole proprietor / single-member LLC", partnership: "Partnership / multi-member LLC", s_corp: "S corporation", c_corp: "C corporation" };
const treatments = {
  personal_paid_by_business: { distribution: "Owner draw or distribution", due_from_owner: "Due from owner (will be repaid)" },
  business_paid_personally: { contribution: "Owner contribution", due_to_owner: "Due to owner (business will reimburse)" },
};
// Suggested account names per entity type; the firm's procedure and chart of accounts decide.
const accounts: Record<EntityType, Record<string, string>> = {
  sole_proprietor: { distribution: "Owner's draw", contribution: "Owner's contribution", due_from_owner: "Due from owner", due_to_owner: "Due to owner" },
  partnership: { distribution: "Partner draws", contribution: "Partner contributions", due_from_owner: "Due from partner", due_to_owner: "Due to partner" },
  s_corp: { distribution: "Shareholder distributions", contribution: "Shareholder contributions", due_from_owner: "Due from shareholder", due_to_owner: "Due to shareholder" },
  c_corp: { distribution: "Dividends paid", contribution: "Additional paid-in capital", due_from_owner: "Due from shareholder", due_to_owner: "Due to shareholder" },
};

export function OwnerResult({ result }: { result: OwnerWorkpaper }) {
  const input = result.input;
  return (
    <section>
      <h3>Owner reclassification · {input.transaction_reference} · {input.close_period}</h3>
      <p>
        {entityTypes[input.entity_type]} · ${input.amount} to {input.payee} on {input.transaction_date} · ledger {input.ledger_reference}
        {input.partner ? ` · partner ${input.partner}` : ""}
      </p>
      {result.blockers.map((b) => <p role="alert" key={b}>{b}</p>)}
      {result.question && <p>Question for the client: {result.question}</p>}
      {input.purpose_status === "answered" && (
        <>
          <p>Client said: “{input.stated_purpose}” ({input.client_confirmation_reference}). Business use {input.business_use_percent}%; reclassified portion ${result.portion}.</p>
          {result.proposal ? (
            <p>Proposed entry: {result.proposal.date} · debit {result.proposal.debit_account}, credit {result.proposal.credit_account}, ${result.proposal.amount} USD.</p>
          ) : (
            <p>No entry: nothing is reclassified at this business-use percentage.</p>
          )}
        </>
      )}
      <p>Draft only. Tax treatment is not decided here, and nothing is posted.</p>
    </section>
  );
}

export default function PrepareOwner() {
  const [params] = useSearchParams();
  const form = useRef<HTMLFormElement>(null);
  const revision = useRevision(form);
  const [entityType, setEntityType] = useState<EntityType | "">("");
  const [direction, setDirection] = useState<OwnerInput["direction"] | "">("");
  const [status, setStatus] = useState<OwnerInput["purpose_status"] | "">("");
  const [treatment, setTreatment] = useState("");
  const [result, setResult] = useState<OwnerWorkpaper>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const saved = revision.savedInput as Partial<OwnerInput> | undefined;
    if (!saved) return;
    setEntityType(saved.entity_type ?? "");
    setDirection(saved.direction ?? "");
    setStatus(saved.purpose_status ?? "");
    setTreatment(saved.treatment ?? "");
  }, [revision.savedInput]);
  useEffect(() => {
    const field = form.current?.elements.namedItem("owner_account");
    if (field instanceof HTMLInputElement && entityType && treatment && !field.value) field.value = accounts[entityType][treatment] ?? "";
  }, [entityType, treatment]);
  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setResult(undefined);
    const data = new FormData(event.currentTarget);
    const text = (name: string) => String(data.get(name) ?? "").trim();
    const answered = status === "answered";
    const input = {
      ...Object.fromEntries(["entity", "close_period", "transaction_reference", "transaction_date", "payee", "amount", "ledger_reference"].map((n) => [n, text(n)])),
      entity_type: entityType,
      direction,
      purpose_status: status,
      business_use_percent: answered ? Number(text("business_use_percent") || 0) : 0,
      method: "owner_reclass_v1",
      ...(answered
        ? {
            ...Object.fromEntries(["stated_purpose", "client_confirmation_reference", "booked_account", "owner_account", "partner", "dividend_declaration_reference", "repayment_reference", "reimbursement_reference"].map((n) => [n, text(n)]).filter(([, v]) => v !== "")),
            treatment,
            not_booked_confirmed: data.has("not_booked_confirmed"),
            treatment_confirmed: data.has("treatment_confirmed"),
          }
        : {}),
    } as OwnerInput;
    try {
      const response = await fetch(`${apiBaseUrl}/v1/owners:prepare`, { method: "POST", headers: { "Content-Type": "application/json", ...(await authenticationHeaders()) }, body: JSON.stringify(input) });
      const value = await response.json();
      if (!response.ok) throw new Error(value.message ?? "Preparation unavailable.");
      setResult(value);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Preparation failed.");
    } finally {
      setBusy(false);
    }
  }
  const field = (name: string, label: string, type = "text", hint?: string) => (
    <label key={name}>
      {label}
      <input name={name} required type={type} maxLength={name === "stated_purpose" ? 500 : 200} defaultValue={name === "entity" ? params.get("entity") ?? "" : name === "close_period" ? params.get("period") ?? "" : ""} />
      {hint && <small>{hint}</small>}
    </label>
  );
  const reset = () => setResult(undefined);
  return (
    <>
      <h1>Owner reclassification</h1>
      <p>Move one owner-related transaction once the client has told you its purpose. If they haven't answered yet, save the question instead; nothing is proposed until they do.</p>
      {revision.close && <SourceReview closeId={revision.close} sources={revision.sources} />}
      {revision.error && <p role="alert">{revision.error}</p>}
      <form ref={form} onSubmit={(e) => void prepare(e)} onChange={reset}>
        <fieldset disabled={busy || !revision.ready}>
          <legend>The transaction</legend>
          <div className="form-grid">
            {field("entity", "Client / entity")}
            {field("close_period", "Close month", "month")}
            <label>
              Client's entity type
              <select value={entityType} onChange={(e) => setEntityType(e.target.value as EntityType)} required>
                <option value="">As recorded under Owner treatments</option>
                {Object.entries(entityTypes).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
              </select>
            </label>
            {field("transaction_reference", "Transaction reference", "text", "For example QuickBooks Purchase 1072")}
            {field("transaction_date", "Transaction date", "date")}
            {field("payee", "Payee")}
            {field("amount", "Amount (USD)")}
            {field("ledger_reference", "Ledger evidence", "text", "Where the ledger shows how it is booked")}
          </div>
          <label>
            What happened?
            <select value={direction} onChange={(e) => { setDirection(e.target.value as OwnerInput["direction"]); setTreatment(""); }} required>
              <option value="">Choose</option>
              <option value="personal_paid_by_business">The business paid a personal cost</option>
              <option value="business_paid_personally">The owner personally paid a business cost</option>
            </select>
          </label>
          <label>
            Has the client confirmed the purpose?
            <select value={status} onChange={(e) => setStatus(e.target.value as OwnerInput["purpose_status"])} required>
              <option value="">Choose</option>
              <option value="open">Not yet — save a question for the client</option>
              <option value="answered">Yes — the client answered</option>
            </select>
          </label>
        </fieldset>
        {status === "answered" && direction && (
          <fieldset disabled={busy || !revision.ready}>
            <legend>The client's answer and treatment</legend>
            <div className="form-grid">
              {field("stated_purpose", "What the client said")}
              {field("client_confirmation_reference", "Where they said it", "text", "Their reply or email")}
              <label>
                Treatment
                <select value={treatment} onChange={(e) => setTreatment(e.target.value)} required>
                  <option value="">Choose</option>
                  {Object.entries(treatments[direction]).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                </select>
              </label>
              <label>
                Business use (%)
                <input name="business_use_percent" type="number" min={0} max={100} required defaultValue={direction === "personal_paid_by_business" ? 0 : 100} />
                <small>From the client's records for mixed-use items</small>
              </label>
              {field("booked_account", direction === "personal_paid_by_business" ? "Account it was booked to" : "Expense account to book")}
              {field("owner_account", "Owner account", "text", "Suggested for the entity type; use the client's chart of accounts")}
              {entityType === "partnership" && (treatment === "distribution" || treatment === "contribution") && field("partner", "Partner", "text", "The owner account must be this partner's")}
              {entityType === "c_corp" && treatment === "distribution" && field("dividend_declaration_reference", "Dividend declaration", "text", "Board resolution or minutes")}
              {treatment === "due_from_owner" && field("repayment_reference", "Repayment confirmation", "text", "Where the owner agreed to repay")}
              {treatment === "due_to_owner" && field("reimbursement_reference", "Reimbursement confirmation", "text", "Where the business agreed to reimburse")}
            </div>
            {direction === "business_paid_personally" && <label><input name="not_booked_confirmed" type="checkbox" required /> The ledger evidence shows this cost is not already booked or reimbursed.</label>}
            <label><input name="treatment_confirmed" type="checkbox" required /> The treatment follows the client's entity type and the firm's policy{entityType === "partnership" ? ", and the owner account is this partner's" : ""}.</label>
          </fieldset>
        )}
        <button className="primary" disabled={busy || !revision.ready}>{busy ? "Checking…" : status === "open" ? "Prepare client question" : "Prepare reclassification"}</button>
      </form>
      {error && <p role="alert">{error}</p>}
      {result && (
        <>
          <OwnerResult result={result} />
          <SaveDraft kind="owner" input={result.input} initialSources={Object.fromEntries(revision.sources.map((s) => [s.role, s]))} />
        </>
      )}
    </>
  );
}
