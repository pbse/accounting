import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { authenticationHeaders } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
import { useCurrency, workspaceRequest } from "../closes/workspace";
import type { EvidenceFile } from "../closes/Evidence";
import { SaveDraft } from "../closes/SaveDraft";
import { useRevision } from "../closes/useRevision";
import type {
  SettlementInput,
  SettlementPayout,
  SettlementWorkpaper,
} from "../../../../../packages/contracts/settlement";

type Chosen = { name: string; text: string; id?: string };

const statusLabel: Record<SettlementPayout["status"], string> = {
  matched: "Candidate deposit",
  reviewed: "Reviewed deposit",
  in_transit: "In transit",
  unmatched: "No deposit found",
  ambiguous: "Choose a deposit",
  failed: "Failed, no deposit expected",
};

const money = (value: string, currency: string) =>
  Number(value).toLocaleString("en-US", { style: "currency", currency });

const exampleReport = `automatic_payout_effective_at,automatic_payout_id,available_on,balance_transaction_id,created,currency,description,fee,gross,net,reporting_category
2026-09-03 00:00:00,po_example_1,2026-09-03 00:00:00,txn_example_1,2026-08-31 10:12:00,usd,Invoice 1041,14.80,500.00,485.20,charge
2026-09-03 00:00:00,po_example_1,2026-09-03 00:00:00,txn_example_2,2026-09-01 15:40:00,usd,Invoice 1042,7.55,250.00,242.45,charge
2026-09-03 00:00:00,po_example_1,2026-09-03 00:00:00,txn_example_3,2026-09-01 16:02:00,usd,Refund of invoice 1039,0.00,-60.00,-60.00,refund
2026-09-17 00:00:00,po_example_2,2026-09-17 00:00:00,txn_example_4,2026-09-14 09:30:00,usd,Invoice 1050,36.55,1250.00,1213.45,charge
2026-09-17 00:00:00,po_example_2,2026-09-17 00:00:00,txn_example_5,2026-09-15 11:00:00,usd,Billing usage fee,0.00,-4.20,-4.20,fee
2026-09-24 00:00:00,po_example_3,2026-09-24 00:00:00,txn_example_6,2026-09-21 13:15:00,usd,Invoice 1055,6.10,200.00,193.90,charge
2026-09-24 00:00:00,po_example_4,2026-09-24 00:00:00,txn_example_7,2026-09-21 13:16:00,usd,Invoice 1056,6.10,200.00,193.90,charge
2026-09-30 00:00:00,po_example_5,2026-09-30 00:00:00,txn_example_8,2026-09-27 08:05:00,usd,Invoice 1060,23.50,800.00,776.50,charge
`;
const exampleBank = `id,date,amount
B-0903,2026-09-03,667.65
B-0911,2026-09-11,-1800.00
B-0918,2026-09-18,1209.25
B-0924,2026-09-24,193.90
B-0925,2026-09-25,193.90
B-1001,2026-10-01,776.50
`;

function PayoutDecision({
  payout,
  input,
  busy,
  onDecide,
}: {
  payout: SettlementPayout;
  input: SettlementInput;
  busy: boolean;
  onDecide: (input: SettlementInput) => void;
}) {
  const [bank, setBank] = useState(payout.candidate_ids[0] ?? "");
  const [reason, setReason] = useState("");
  const [policy, setPolicy] = useState("");
  const explained = reason.trim().length >= 5;
  const useDeposit = () =>
    onDecide({
      ...input,
      reviewed_deposits: [
        ...(input.reviewed_deposits ?? []),
        { payout_id: payout.id, bank_id: bank.trim(), reason },
      ],
    });
  return (
    <fieldset disabled={busy}>
      <legend>
        {payout.id} · {money(payout.net, input.currency)} expected {payout.expected_date}
      </legend>
      {payout.candidate_ids.length > 0 ? (
        <div role="radiogroup" aria-label={`Bank deposit for ${payout.id}`}>
          {payout.candidate_ids.map((id) => (
            <label className="checkbox-label" key={id}>
              <input
                type="radio"
                name={`deposit-${payout.id}`}
                checked={bank === id}
                onChange={() => setBank(id)}
              />
              <span>Bank row {id}</span>
            </label>
          ))}
        </div>
      ) : (
        <div className="form-grid">
          <label>
            Accountant-approved in-transit rule
            <input
              value={policy}
              maxLength={200}
              onChange={(e) => setPolicy(e.target.value)}
              placeholder="Firm deposits-in-transit policy v2"
            />
          </label>
          <label>
            Bank row with this exact amount (optional)
            <input
              value={bank}
              maxLength={100}
              onChange={(e) => setBank(e.target.value)}
              placeholder="Only if it posted on another date"
            />
          </label>
        </div>
      )}
      <label>
        What did you verify?
        <textarea
          value={reason}
          minLength={5}
          maxLength={500}
          rows={2}
          onChange={(e) => setReason(e.target.value)}
          placeholder="For example: Stripe trace ID matches the bank reference, or the next statement shows this deposit on Oct 2."
        />
      </label>
      <div className="export-actions">
        <button
          type="button"
          disabled={!explained || !bank.trim()}
          onClick={useDeposit}
        >
          Use this deposit
        </button>
        {payout.candidate_ids.length === 0 && (
          <button
            type="button"
            disabled={!explained || policy.trim().length < 5}
            onClick={() =>
              onDecide({
                ...input,
                in_transit: [
                  ...(input.in_transit ?? []),
                  { payout_id: payout.id, policy_reference: policy, reason },
                ],
              })
            }
          >
            Mark in transit at month-end
          </button>
        )}
      </div>
    </fieldset>
  );
}

export function SettlementResult({
  result,
  busy = false,
  onDecide,
}: {
  result: SettlementWorkpaper;
  busy?: boolean;
  onDecide?: (input: SettlementInput) => void;
}) {
  const { input, payouts } = result;
  const open = payouts.filter(
    (p) => p.status === "unmatched" || p.status === "ambiguous",
  );
  const decided = payouts.filter(
    (p) => p.status === "reviewed" || p.status === "in_transit",
  );
  const late = (p: SettlementPayout) =>
    !!p.deposit && !p.deposit.date.startsWith(input.close_period);
  const headline =
    payouts.length === 0
      ? "No automatic payouts in this export."
      : open.length === 0 && result.unassigned.length > 0
        ? `All ${payouts.length} payouts traced to ${input.bank_account}, but ${result.unassigned.length} Stripe ${result.unassigned.length === 1 ? "row has" : "rows have"} no payout ID and still ${result.unassigned.length === 1 ? "blocks" : "block"} approval.`
        : open.length === 0
          ? `All ${payouts.length} payouts traced to ${input.bank_account}.`
          : `${payouts.length - open.length} of ${payouts.length} payouts traced to ${input.bank_account}. ${open.length} ${open.length === 1 ? "needs" : "need"} your decision.`;
  const undo = (payout: SettlementPayout) =>
    onDecide?.({
      ...input,
      reviewed_deposits: input.reviewed_deposits?.filter(
        (d) => d.payout_id !== payout.id,
      ),
      in_transit: input.in_transit?.filter((d) => d.payout_id !== payout.id),
    });
  const lineage = result.method !== "stripe_payouts_v1";
  return (
    <>
      <h3>{headline}</h3>
      <p>
        {result.blockers.length === 0
          ? "Check the candidate deposits against the bank statement, then approve the saved workpaper."
          : "Unresolved payouts keep this workpaper from approval. Nothing is balanced or plugged automatically."}
      </p>
      <div className="review-grid">
        <section>
          <h4>Stripe paid out</h4>
          {result.method === "stripe_payouts_v1" ? (
            <dl>
              <dt>Paid out</dt>
              <dd>{money(result.total.net, input.currency)}</dd>
              <dt>Payouts</dt>
              <dd>{payouts.length}</dd>
            </dl>
          ) : (
            <dl>
              <dt>Gross activity</dt>
              <dd>{money(result.total.gross, input.currency)}</dd>
              <dt>Fees on transactions</dt>
              <dd>{money(result.total.fee, input.currency)}</dd>
              <dt>Net paid out</dt>
              <dd>{money(result.total.net, input.currency)}</dd>
              <dt>Payouts · transactions</dt>
              <dd>
                {payouts.length} · {result.total.count}
              </dd>
            </dl>
          )}
        </section>
        <section>
          <h4>Sources and method</h4>
          <dl>
            <dt>Stripe export</dt>
            <dd>{input.report_reference}</dd>
            <dt>Bank file</dt>
            <dd>
              {input.bank_reference} · {input.bank_account}
            </dd>
            <dt>Method</dt>
            <dd>
              {result.method === "stripe_payouts_v1"
                ? `${input.currency} payouts from the Payouts report, without their transactions; exact amount, one day before to four days after the expected arrival.`
                : `${input.currency} automatic payouts; exact net amount, one day before to four days after the expected date.`}
            </dd>
          </dl>
        </section>
      </div>
      {result.blockers.length > 0 && (
        <section>
          <h4>Needs your decision</h4>
          <ul>
            {result.blockers.map((b) => (
              <li key={b}>{b}</li>
            ))}
          </ul>
        </section>
      )}
      {result.questions.length > 0 && (
        <section>
          <h4>Check before recording</h4>
          <ul>
            {result.questions.map((q) => (
              <li key={q}>{q}</li>
            ))}
          </ul>
        </section>
      )}
      <section>
        <table>
          <caption>
            {lineage
              ? `Payouts expected in ${input.close_period}. Fees are what Stripe kept from each batch; net is what should reach the bank.`
              : `Payouts whose funds left the Stripe balance in ${input.close_period}, with the amount that should reach the bank.`}
          </caption>
          <thead>
            <tr>
              <th>Payout</th>
              <th>{lineage ? "Expected" : "Expected arrival"}</th>
              {lineage && <th>Transactions</th>}
              {lineage && <th>Gross</th>}
              {lineage && <th>Fees</th>}
              <th>{lineage ? "Net" : "Amount"}</th>
              <th>Bank row</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {payouts.map((p) => (
              <tr key={p.id}>
                <th>{p.id}</th>
                <td>{p.expected_date}</td>
                {lineage && <td>{p.count}</td>}
                {lineage && <td>{money(p.gross, input.currency)}</td>}
                {lineage && <td>{money(p.fee, input.currency)}</td>}
                <td>{money(p.net, input.currency)}</td>
                <td>
                  {p.deposit
                    ? `${p.deposit.id} · ${p.deposit.date}${late(p) ? " · after month-end" : ""}`
                    : p.candidate_ids.length > 0
                      ? `${p.candidate_ids.join(" or ")}?`
                      : "—"}
                </td>
                <td>
                  <span
                    className={
                      p.status === "unmatched" || p.status === "ambiguous"
                        ? "pill muted"
                        : "pill"
                    }
                  >
                    {statusLabel[p.status]}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
      {onDecide && open.length > 0 && (
        <section>
          <h4>Resolve the open payouts</h4>
          <p>
            Choose the deposit you verified, or mark a payout that landed after
            month-end as in transit under the accountant's rule. Each choice
            recalculates the draft.
          </p>
          {open.map((p) => (
            <PayoutDecision
              key={`${p.id}-${p.candidate_ids.join()}`}
              payout={p}
              input={input}
              busy={busy}
              onDecide={onDecide}
            />
          ))}
        </section>
      )}
      {decided.length > 0 && (
        <section>
          <h4>Reviewer decisions</h4>
          <ul>
            {decided.map((p) => (
              <li key={p.id}>
                {p.id}:{" "}
                {p.status === "reviewed"
                  ? `bank row ${p.deposit?.id} (${p.deposit?.date})`
                  : `in transit under ${p.policy_reference}`}{" "}
                · {p.reason}{" "}
                {onDecide && (
                  <button type="button" disabled={busy} onClick={() => undo(p)}>
                    Undo
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      )}
      {result.method !== "stripe_payouts_v1" && <details>
        <summary>Stripe categories behind these payouts</summary>
        <table>
          <thead>
            <tr>
              <th>Category</th>
              <th>Transactions</th>
              <th>Gross</th>
              <th>Fees</th>
              <th>Net</th>
            </tr>
          </thead>
          <tbody>
            {[...result.categories, result.total].map((c) => (
              <tr key={c.category}>
                <th>{c.category === "all" ? "Total" : c.category}</th>
                <td>{c.count}</td>
                <td>{money(c.gross, input.currency)}</td>
                <td>{money(c.fee, input.currency)}</td>
                <td>{money(c.net, input.currency)}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {payouts.map((p) => (
          <p key={p.id} className="source-label">
            {p.id}:{" "}
            {p.categories
              .map((c) => `${c.count} ${c.category} ${money(c.net, input.currency)}`)
              .join(" · ")}
          </p>
        ))}
      </details>}
      {input.fee_entry && (
        <section>
          <h4>Stripe fee entry</h4>
          <p>
            Fees of payouts deposited in {input.close_period}: {money(result.fees ?? "0.00", input.currency)}; already booked: {money(input.fee_entry.booked_fees, input.currency)}. Rule: {input.fee_entry.rule_reference}.
          </p>
          {result.proposal ? (
            <p>
              Proposed {result.proposal.date}: debit {result.proposal.debit_account}, credit {result.proposal.credit_account}, {money(result.proposal.amount, input.currency)}.
            </p>
          ) : (
            <p>{result.blockers.length > 0 ? "No entry is proposed until every payout is traced." : "No entry is needed; the booked fees match."}</p>
          )}
        </section>
      )}
      <p className="note">
        Draft workpaper. A candidate deposit is a lead, not a confirmed match,
        until an accountant approves this workpaper.{" "}
        {input.fee_entry
          ? "Nothing is posted; book the approved fee entry in QuickBooks."
          : "No journal is proposed; record gross revenue and fees under the client's approved method."}
      </p>
    </>
  );
}

export default function Prepare() {
  const [params] = useSearchParams();
  const currency = useCurrency(params.get("close"));
  const close = params.get("close");
  const form = useRef<HTMLFormElement>(null);
  const revision = useRevision(form);
  const heading = useRef<HTMLHeadingElement>(null);
  const preselected = useRef(false);
  const latest = useRef({ report: "", bank: "" });
  const [files, setFiles] = useState<EvidenceFile[]>([]);
  const [report, setReport] = useState<Chosen>();
  const [bank, setBank] = useState<Chosen>();
  const [autoPicked, setAutoPicked] = useState("");
  const [result, setResult] = useState<SettlementWorkpaper>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [feeEntry, setFeeEntry] = useState(false);
  const [manual, setManual] = useState(false);
  useEffect(() => {
    if (revision.savedInput) setManual(revision.savedInput.method === "stripe_payouts_v1");
  }, [revision.savedInput]);
  const csvFiles = files.filter(
    (f) =>
      !f.superseded_by &&
      f.extraction_state === "ready" &&
      f.name.toLowerCase().endsWith(".csv"),
  );

  useEffect(() => {
    if (result) heading.current?.focus();
  }, [result]);
  useEffect(() => {
    if (close)
      workspaceRequest(`/${close}/evidence`)
        .then(setFiles)
        .catch((e) => setError(e.message));
  }, [close]);

  async function choose(id: string, slot: "report" | "bank") {
    const set = slot === "report" ? setReport : setBank;
    latest.current[slot] = id;
    set(undefined);
    const file = files.find((f) => f.id === id);
    if (!file || !close) return;
    try {
      const blob = await workspaceRequest(
        `/${close}/evidence/${id}/text`,
        undefined,
        true,
      );
      const text = await blob.text();
      if (latest.current[slot] === id) set({ id, name: file.name, text });
    } catch (e) {
      setError(e instanceof Error ? e.message : "Evidence is unavailable.");
    }
  }
  useEffect(() => {
    if (preselected.current || !revision.ready || csvFiles.length === 0) return;
    preselected.current = true;
    const saved = (role: string) =>
      revision.sources.find((s) => s.role === role)?.id;
    const reportId = saved("processor_report") ?? params.get("report");
    let bankId = saved("bank");
    const others = csvFiles.filter((f) => f.id !== reportId);
    if (!bankId && reportId && others.length === 1) {
      bankId = others[0].id;
      setAutoPicked(others[0].name);
    }
    if (reportId) void choose(reportId, "report");
    if (bankId) void choose(bankId, "bank");
  }, [revision.ready, files]);

  async function calculate(input: SettlementInput) {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(`${apiBaseUrl}/v1/settlements:prepare`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(await authenticationHeaders()),
        },
        body: JSON.stringify(input),
      });
      const paper = await response.json();
      if (!response.ok) throw new Error(paper.message ?? "Tracing failed.");
      setResult(paper);
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Tracing is unavailable. Your choices are still here.",
      );
    } finally {
      setBusy(false);
    }
  }
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!report || !bank) return;
    const data = new FormData(event.currentTarget);
    const text = (key: string) => String(data.get(key) ?? "");
    setResult(undefined);
    void calculate({
      ...(manual ? { method: "stripe_payouts_v1" as const } : {}),
      entity: text("entity"),
      close_period: text("close_period"),
      currency,
      bank_account: text("bank_account"),
      report_reference: report.name,
      bank_reference: bank.name,
      report_csv: report.text,
      bank_csv: bank.text,
      method_confirmed: data.has("method_confirmed"),
      coverage_confirmed: data.has("coverage_confirmed"),
      reviewed_deposits: [],
      in_transit: [],
      ...(feeEntry && !manual
        ? {
            fee_entry: {
              fee_account: text("fee_account"),
              deposit_account: text("deposit_account"),
              booked_fees: text("booked_fees"),
              rule_reference: text("rule_reference"),
            },
          }
        : {}),
    });
  }
  function readFile(file: File | undefined, set: (value?: Chosen) => void) {
    setError("");
    if (!file) return set(undefined);
    if (file.size > 512 * 1024)
      return setError("Choose a CSV of at most 512 KiB.");
    void file.text().then((text) => set({ name: file.name, text }));
  }
  function example() {
    const fields = form.current!.elements;
    for (const [name, value] of Object.entries({
      entity: "Acme Services (example)",
      close_period: "2026-09",
      bank_account: "Operating checking",
    }))
      (fields.namedItem(name) as HTMLInputElement).value = value;
    for (const name of ["method_confirmed", "coverage_confirmed"])
      (fields.namedItem(name) as HTMLInputElement).checked = false;
    setReport({ name: "Synthetic Stripe payout export", text: exampleReport });
    setBank({ name: "Synthetic bank CSV", text: exampleBank });
    setResult(undefined);
    setError("");
  }
  const picker = (label: string, hint: string, slot: "report" | "bank") => {
    const chosen = slot === "report" ? report : bank;
    const set = slot === "report" ? setReport : setBank;
    return (
      <label>
        {label}
        {close ? (
          <select
            value={chosen?.id ?? latest.current[slot]}
            required
            onChange={(e) => {
              setAutoPicked("");
              void choose(e.target.value, slot);
            }}
          >
            <option value="">Choose a CSV from this close</option>
            {csvFiles.map((f) => (
              <option key={f.id} value={f.id}>
                {f.name} · #{f.id}
              </option>
            ))}
          </select>
        ) : (
          <input
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => readFile(e.target.files?.[0], set)}
          />
        )}
        <small>
          {chosen && !close && <strong>Loaded {chosen.name}. </strong>}
          {hint}
        </small>
      </label>
    );
  };
  return (
    <>
      <p className="eyebrow">STRIPE SETTLEMENT / PREPARATION</p>
      <h1>Every Stripe payout, traced to the bank.</h1>
      <p className="intro">
        Stripe deposits charges, refunds, disputes and fees as one net payout.
        Bring Stripe's itemized payout export and the bank account's CSV; each
        payout is matched to its deposit, and anything unresolved stays visible.
      </p>
      {revision.paper && (
        <p role="status">
          {revision.error ||
            (revision.ready
              ? `Revising draft #${revision.paper}. Files are reselected from its current evidence; confirm the method again. Earlier drafts and reviews stay in history.`
              : "Loading the saved draft and its current evidence…")}
        </p>
      )}
      <details className="preparation-inputs" open={!result}>
        <summary>
          {result
            ? "Change files and trace again"
            : "Stripe export and bank file"}
        </summary>
        <form ref={form} onSubmit={submit}>
          <fieldset disabled={busy || !revision.ready}>
            <legend>1. Choose the two files</legend>
            <div className="form-grid">
              <label>
                Client
                <input
                  name="entity"
                  required
                  maxLength={200}
                  readOnly={!!close}
                  defaultValue={params.get("entity") ?? ""}
                />
              </label>
              <label>
                Close month
                <input
                  name="close_period"
                  type="month"
                  required
                  readOnly={!!close}
                  defaultValue={params.get("period") ?? ""}
                />
              </label>
              <label>
                Payout schedule
                <select value={manual ? "manual" : "automatic"} onChange={(e) => { setManual(e.target.value === "manual"); setResult(undefined); }}>
                  <option value="automatic">Automatic payouts</option>
                  <option value="manual">Every payout from the Payouts report (manual, instant or mixed)</option>
                </select>
              </label>
              {picker(
                manual ? "Stripe Payouts report" : "Stripe payout export",
                manual
                  ? "Stripe Dashboard → Reports → Balance → Payouts → Export → Itemized CSV, for this month."
                  : "Stripe Dashboard → Reports → Payout reconciliation → Download → Itemized. Use the full download, not one category.",
                "report",
              )}
              {picker(
                "Bank CSV",
                manual
                  ? "Columns id,date,amount with deposits positive. Run it through the later of four days after month-end and four days after the last payout's expected arrival, so late deposits can match."
                  : "Columns id,date,amount with deposits positive. Include the first four days of next month so late deposits can match.",
                "bank",
              )}
              <label>
                Bank account
                <input
                  name="bank_account"
                  required
                  maxLength={200}
                  placeholder="Operating checking"
                />
              </label>
            </div>
            {autoPicked && (
              <p className="source-label" role="status">
                Picked {autoPicked}, the only other CSV in this close. Change it
                if that is not the bank file.
              </p>
            )}
            {close && files.length > 0 && csvFiles.length < 2 && (
              <p>
                Upload both CSVs to the{" "}
                <Link to={`/dashboard/closes/${close}`}>client close</Link>{" "}
                first.
              </p>
            )}
          </fieldset>
          <fieldset disabled={busy || !revision.ready}>
            <legend>2. Confirm this method applies</legend>
            <label className="checkbox-label">
              <input type="checkbox" name="method_confirmed" required />
              <span>
                {manual
                  ? `${currency} payouts from the Payouts report, whatever their schedule. Each payout is traced to a bank row carrying exactly its amount from one day before to four days after its expected arrival. Stripe cannot say which transactions a manual or instant payout settles, so the month's activity must be reconciled separately from the Balance report.`
                  : `${currency} automatic payouts. Stripe's payout ID defines each batch; a bank row is only a candidate when it carries exactly the payout's net amount from one day before to four days after the expected date.`}
              </span>
            </label>
            <label className="checkbox-label">
              <input type="checkbox" name="coverage_confirmed" required />
              <span>
                The Stripe file is the full itemized download for this month,
                and the bank file covers the whole month.
              </span>
            </label>
          </fieldset>
          {!manual && (
          <fieldset disabled={busy || !revision.ready}>
            <legend>3. Stripe fee entry (optional)</legend>
            <label className="checkbox-label">
              <input type="checkbox" checked={feeEntry} onChange={(e) => setFeeEntry(e.target.checked)} />
              <span>
                The client books Stripe deposits net of fees, and their accountant's rule says to move the fees to an expense account. Only fees of payouts deposited this month count.
              </span>
            </label>
            {feeEntry && (
              <div className="form-grid">
                <label>
                  Fee expense account
                  <input name="fee_account" required maxLength={200} />
                </label>
                <label>
                  Account the net deposits were booked to
                  <input name="deposit_account" required maxLength={200} />
                </label>
                <label>
                  Fees already booked this month ({currency}; enter zero explicitly)
                  <input name="booked_fees" required inputMode="decimal" />
                </label>
                <label>
                  Accountant's rule reference
                  <input name="rule_reference" required minLength={5} maxLength={200} />
                </label>
              </div>
            )}
          </fieldset>
          )}
          {error && !result && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <div className="export-actions">
            <button
              className="primary"
              disabled={busy || !revision.ready || !report || !bank}
            >
              {busy ? "Tracing payouts…" : "Trace payouts →"}
            </button>
            {!close && (
              <button
                type="button"
                className="example-button"
                onClick={example}
              >
                Use the synthetic example
              </button>
            )}
          </div>
        </form>
      </details>
      {result && (
        <div className="prepared-result">
          <p className="eyebrow">
            PREPARED DRAFT / {result.input.close_period}
          </p>
          <h2 ref={heading} tabIndex={-1}>
            {result.input.entity} · Stripe settlement
          </h2>
          {error && (
            <p className="form-error" role="alert">
              {error}
            </p>
          )}
          <SettlementResult result={result} busy={busy} onDecide={calculate} />
          <div className="export-actions">
            <button
              onClick={() => {
                const url = URL.createObjectURL(
                  new Blob([JSON.stringify(result, null, 2)], {
                    type: "application/json",
                  }),
                );
                const link = document.createElement("a");
                link.href = url;
                link.download = "stripe-settlement-workpaper.json";
                link.click();
                setTimeout(() => URL.revokeObjectURL(url), 0);
              }}
            >
              Export workpaper JSON
            </button>
          </div>
          <SaveDraft
            kind="settlement"
            input={result.input}
            initialSources={
              report?.id && bank?.id
                ? {
                    processor_report: { id: report.id, location: "All rows" },
                    bank: { id: bank.id, location: "All rows" },
                  }
                : {}
            }
          />
        </div>
      )}
    </>
  );
}
