import { useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { apiBaseUrl } from "../../lib/api";
import { authenticationHeaders } from "../auth/Auth";
import { revisionSources, SaveDraft } from "../closes/SaveDraft";
import { money, useCurrency } from "../closes/workspace";
import { SourceReview } from "../closes/Evidence";
import { useRevision } from "../closes/useRevision";
import type { AccrualInput, AccrualWorkpaper } from "../../../../../packages/contracts/accrual";
import { PageHeader } from "@close/ui/components/PageHeader";

export function AccrualConfirmations({ proRata = true }: { proRata?: boolean }) {
 return <>
  <label>Accrual method<select name="method" required defaultValue=""><option value="">Select approved method</option><option value="fixed_incurred_amount_v1">Fixed incurred closing amount · v1</option><option value="actual_invoice_true_up_v1">Actual invoice vs prior estimate · v1</option><option value="fixed_recurring_incurred_v1">Fixed recurring incurred service · v1</option>{proRata && <option value="pro_rata_service_days_v1">Pro-rata share of a service contract · v1</option>}</select></label>
  <details><summary>Synthetic accrual examples · version 1</summary><p>Incurred service estimate $1,200; booked liability $200: debit expense / credit accrued liability $1,000 at September 30. If selected, October 1 reverses only that $1,000. If the booked balance is $1,400, the correcting entry debits liability / credits expense $200. If already $1,200, no entry or reversal is proposed.</p><p>Actual invoice $1,250 against an earlier $1,200 estimate: variance +$50. If the confirmed booked liability is $1,200 and the approved closing liability is $1,250, propose debit expense / credit liability $50. If the invoice has already brought the booked liability to $1,250, propose no entry. An invoice payment, reversal or prior-period correction must be reflected in the confirmed booked balance; this draft does not post or revise an earlier close.</p><p>The accountant supplies and supports both amounts. The agent cannot infer an expense from a missing invoice. Confirm prior-period treatment and materiality in the client's approved procedure.</p></details>
  {proRata && <p>Pro-rata example: a 9,000 maintenance contract for August 15–November 14, 2026 (92 days) has 47 days elapsed at September 30, so 4,597.82 is incurred (cents rounded down). With 3,000 already invoiced and booked outside the accrued liability, the target liability is 1,597.82; against a booked 1,000 the draft proposes 597.82.</p>}
  <p>Recurring example: a client-approved monthly service amount of $1,200, with $200 already booked for this month, proposes a $1,000 adjustment. In the next month, the booked liability must be checked again; the prior month’s approval does not carry over.</p>
  <details><summary>Method-specific rule and actual-invoice references</summary><p>For recurring work, cite the client accountant’s approved recurring rule. For an actual invoice, cite that approved rule and distinct estimate and actual sources.</p>
    <label>Prior estimate amount<input name="estimate_amount" inputMode="decimal" maxLength={13} /></label>
    <label>Prior estimate workpaper or source reference<input name="estimate_reference" maxLength={200} /></label>
    <label>Actual invoice source reference<input name="actual_reference" maxLength={200} /></label>
    <label>Client accountant's approved rule reference<input name="accountant_rule_reference" maxLength={200} /></label>
    {proRata && <><label>Pro-rata: contract amount for the whole service period<input name="contract_amount" inputMode="decimal" maxLength={13} /></label>
    <label>Pro-rata: invoiced to date and booked outside the accrued liability (0.00 if none)<input name="invoiced_to_date" inputMode="decimal" maxLength={13} /></label></>}
  </details>
  <label>Reverse this proposed adjustment next month?<select name="reverse_next_month" required defaultValue=""><option value="">Choose explicitly</option><option value="false">No automatic reversal proposal</option><option value="true">Propose reversal on next month's first day</option></select></label>
  <label><input name="examples_confirmed" type="checkbox" required /> I reviewed the examples and the client's approved method.</label>
  <label><input name="method_confirmed" type="checkbox" required /> Confirm accrual basis in the client's home currency, the supported incurred amount, estimation basis, materiality treatment and explicit reversal choice. No future service is included; a pro-rata contract accrues only its days elapsed by month-end.</label>
  <label><input name="booking_confirmed" type="checkbox" required /> Confirm the booked liability is complete through month-end, including all invoices and prior adjustments. No duplicate unpaid amount is included.</label>
 </>;
}
export function AccrualResult({result}:{result:AccrualWorkpaper}) {
 return <section><h3>Draft accrual · {result.input.close_period}</h3><p>Method: {result.input.method}. Source: {result.input.source_reference}.</p><p>Service: {result.input.service_start} to {result.input.service_end}. Basis: {result.input.estimation_basis}.</p><p>Target liability: {money(result.input.amount, result.input.currency)}; booked: {money(result.input.booked_balance, result.input.currency)} ({result.input.booking_reference}).</p>
  {[result.proposal,result.reversal].map((entry,index)=>entry && <p key={index}>{index ? "Proposed reversal" : "Proposed adjustment"}: {entry.date} · debit {entry.debit_account}, credit {entry.credit_account}, {money(entry.amount, result.input.currency)}.</p>)}
  {!result.proposal && <p>Booked liability matches the target; no adjustment or reversal proposed.</p>}
  <p>Draft only. Source review and accountant approval are required. Nothing is posted or automatically reversed.</p>
  {result.input.method === "pro_rata_service_days_v1" && <p>Contract {money(result.input.contract_amount, result.input.currency)} for {result.input.service_start} to {result.input.service_end}, pro rata by service days through month-end, less {money(result.input.invoiced_to_date, result.input.currency)} invoiced to date. Approved rule: {result.input.accountant_rule_reference}.</p>}
  {result.input.method === "actual_invoice_true_up_v1" && <p>Prior estimate: {money(result.input.estimate_amount, result.input.currency)} ({result.input.estimate_reference}). Actual invoice: {result.input.actual_reference}. Variance: {money(result.estimate_variance, result.input.currency)}. Approved rule: {result.input.accountant_rule_reference}.</p>}
 </section>;
}
export default function PrepareAccrual() {
 const [params]=useSearchParams();const currency=useCurrency(params.get("close"));const form=useRef<HTMLFormElement>(null);const revision=useRevision(form);
 const [result,setResult]=useState<AccrualWorkpaper>();const [error,setError]=useState("");const [busy,setBusy]=useState(false);
 function example() {
  const values={entity:params.get("entity")??"Synthetic accounting methods",source_reference:"Synthetic completed-service example v1",close_period:"2028-02",service_start:"2028-02-01",service_end:"2028-02-29",amount:"1200.00",booked_balance:"200.00",booking_reference:"Synthetic February ledger",estimation_basis:"12 approved completed hours at $100",expense_account:"Consulting expense",liability_account:"Accrued consulting",method:"fixed_incurred_amount_v1",reverse_next_month:"true"};
  for(const [name,value] of Object.entries(values)){const field=form.current?.elements.namedItem(name);if(field instanceof HTMLInputElement || field instanceof HTMLSelectElement)field.value=value;}
  for(const name of ["examples_confirmed","method_confirmed","booking_confirmed"]){const field=form.current?.elements.namedItem(name);if(field instanceof HTMLInputElement)field.checked=false;}
  setResult(undefined);setError("");
 }
 async function prepare(event:FormEvent<HTMLFormElement>) {
  event.preventDefault();setBusy(true);setError("");setResult(undefined);
  const data=new FormData(event.currentTarget);const text=(name:string)=>String(data.get(name)??"");
  const input={...Object.fromEntries(["entity","source_reference","close_period","service_start","service_end","amount","booked_balance","booking_reference","estimation_basis","expense_account","liability_account","method","estimate_amount","estimate_reference","actual_reference","accountant_rule_reference"].map(k=>[k,text(k)])),...(text("method")==="pro_rata_service_days_v1" && {contract_amount:text("contract_amount"),invoiced_to_date:text("invoiced_to_date")}),currency,basis:"accrual",method_confirmed:data.has("method_confirmed"),booking_confirmed:data.has("booking_confirmed"),examples_confirmed:data.has("examples_confirmed"),reverse_next_month:text("reverse_next_month")==="true"} as AccrualInput;
  try {const response=await fetch(`${apiBaseUrl}/v1/accruals:prepare`,{method:"POST",headers:{"Content-Type":"application/json",...await authenticationHeaders()},body:JSON.stringify(input)});const value=await response.json();if(!response.ok)throw new Error(value.message??"Preparation unavailable.");setResult(value);}catch(e){setError(e instanceof Error?e.message:"Preparation failed.");}finally{setBusy(false);}
 }
 return <><PageHeader title="Accrued expense">Confirm incurred services, an evidence-based closing amount and the actual booked liability. Only the difference is proposed.</PageHeader>
  {revision.close && <SourceReview closeId={revision.close} sources={revision.sources} />}
  {revision.error && <p role="alert">{revision.error}</p>}
  <form ref={form} onSubmit={e=>void prepare(e)} onChange={()=>setResult(undefined)}><fieldset disabled={busy||!revision.ready}><legend>Incurred service and ledger evidence</legend><button type="button" onClick={example}>Use synthetic accrual example</button>
   {[['entity','Client / entity'],['source_reference','Source document and location'],['close_period','Close month'],['service_start','Service starts'],['service_end','Service ends'],['amount',`Supported closing accrual (${currency}; leave blank for pro-rata)`],['booked_balance',`Booked liability credit balance (${currency}; enter zero explicitly)`],['booking_reference','Ledger balance and cutoff reference'],['estimation_basis','Evidence and approved estimate basis'],['expense_account','Expense account'],['liability_account','Accrued liability account']].map(([name,label])=><label key={name}>{label}<input name={name} required={name!=='amount'} maxLength={200} type={name==='close_period'?'month':name.startsWith('service_')?'date':'text'} defaultValue={name==='entity'?(params.get('entity')??''):name==='close_period'?(params.get('period')??''):''} /></label>)}
   <AccrualConfirmations /><button>{busy?'Preparing…':'Prepare accrual draft'}</button>
  </fieldset></form>{error && <p role="alert">{error}</p>}{result && <><AccrualResult result={result} /><SaveDraft kind="accrual" input={result.input} initialSources={revisionSources(revision.sources).byRole} keptSources={revisionSources(revision.sources).kept} /></>}
 </>;
}
