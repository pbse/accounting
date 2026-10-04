import { useRevision } from "../closes/useRevision";
import { SourceReview } from "../closes/Evidence";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { authenticationHeaders } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
import { useCurrency, workspaceRequest } from "../closes/workspace";
import type { EvidenceFile } from "../closes/Evidence";
import { SaveDraft } from "../closes/SaveDraft";
import type {
  ReconciliationInput,
  ReconciliationWorkpaper,
  ReconciliationLine,
  ReviewedMatch,
} from "../../../../../packages/contracts/reconciliation";
import { PageHeader } from "@close/ui/components/PageHeader";

type PriorOutstanding = NonNullable<ReconciliationInput["prior_outstanding"]>[number];

function Lines({
  title,
  lines,
}: {
  title: string;
  lines: ReconciliationLine[];
}) {
  return (
    <section>
      <h3>
        {title} · {lines.length}
      </h3>
      {lines.length === 0 ? (
        <p>None in the imported rows.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Date</th>
              <th>Signed amount</th>
            </tr>
          </thead>
          <tbody>
            {lines.map((line) => (
              <tr key={line.id}>
                <th>{line.id}</th>
                <td>{line.date}</td>
                <td>{line.amount}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
export function ReconciliationResult({
  result,
}: {
  result: ReconciliationWorkpaper;
}) {
  const exceptions = result.exceptions ?? [];
  return (
    <>
      <p className="note">
        Draft workpaper only. Unreviewed exact matches are candidates, not
        confirmed matches or native reconciliation. Source references and page
        inventory are user-supplied; documents have not been independently
        inspected.
      </p>
      <p>
        {result.input.entity} · {result.input.account} ·{" "}
        {result.input.close_period} · {result.input.currency} · {result.input.account_type === "credit_card" ? "card liability" : "bank cash"}
      </p>
      <p>
        Statement: {result.input.statement_reference}. Ledger:{" "}
        {result.input.ledger_reference}.
      </p>
      <h3>
        {result.blockers.length
          ? "Needs investigation"
          : "Imported balances tie; review source completeness and match candidates"}
      </h3>
      {result.blockers.length > 0 && (
        <ul>
          {result.blockers.map((item) => (
            <li key={item}>{item}</li>
          ))}
        </ul>
      )}
      <dl>
        <dt>Statement roll-forward difference</dt>
        <dd>${result.statement_roll_forward_difference}</dd>
        <dt>Ledger roll-forward difference</dt>
        <dd>${result.ledger_roll_forward_difference}</dd>
        <dt>Opening statement minus ledger</dt>
        <dd>${result.opening_difference}</dd>
        <dt>Closing statement minus ledger</dt>
        <dd>${result.closing_difference}</dd>
        <dt>Opening bridge after cited prior items</dt>
        <dd>${result.opening_bridge_difference}</dd>
        <dt>Closing bridge after reviewed timing and clearing items</dt>
        <dd>${result.closing_bridge_difference}</dd>
        <dt>Reviewer-observed QuickBooks status (not API-verified)</dt>
        <dd>{result.input.native_status || "unverified"}{result.input.native_status_reference ? ` · ${result.input.native_status_reference}` : ""}</dd>
      </dl>
      {(result.input.prior_outstanding?.length || result.input.current_outstanding?.length || result.input.transfers?.length) ? <section>
        <h3>Reviewed timing, clearing and transfer instructions</h3>
        <p>These draft classifications use the cited accountant rules; they do not change the ledger or QuickBooks status.</p>
        <ul>
          {result.input.prior_outstanding?.map((item, index) => <li key={`prior-${index}`}>Prior {item.treatment ?? "timing"} {item.side} {item.id}: ${item.amount} · {item.cleared_by ? `cleared by ${item.cleared_by}` : "still outstanding"} · {item.source_reference} · {item.clearing_reference ? `clearing source ${item.clearing_reference} · ` : ""}rule {item.policy_reference}</li>)}
          {result.input.current_outstanding?.map((item, index) => <li key={`current-${index}`}>Current {item.treatment ?? "timing"} {item.side} {item.id} · {item.clearing_reference ? `clearing source ${item.clearing_reference} · ` : ""}rule {item.policy_reference} · {item.reason}</li>)}
          {result.input.transfers?.map((item, index) => <li key={`transfer-${index}`}>Transfer {item.statement_id}/{item.ledger_id} ↔ {item.other_statement_id}/{item.other_ledger_id} in {result.input.other_account} · rule {item.policy_reference}</li>)}
        </ul>
      </section> : null}
      <h3>Exact-match candidates · {result.matches.length}</h3>
      <p>
        Same date and signed amount, unique on both sides. Duplicate amounts on
        the same date remain unmatched.
      </p>
      {result.matches.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Statement ID</th>
              <th>Ledger ID</th>
              <th>Reason</th>
            </tr>
          </thead>
          <tbody>
            {result.matches.map((match) => (
              <tr key={match.statement_id}>
                <th>{match.statement_id}</th>
                <td>{match.ledger_id}</td>
                <td>{match.reason}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {(result.reviewed_matches?.length ?? 0) > 0 && <section>
        <h3>Reviewer-selected groups · {result.reviewed_matches.length}</h3>
        <p>These choices remain draft work until the saved workpaper receives its separate accountant review.</p>
        <ul>{result.reviewed_matches.map((match, index) => <li key={index}>{match.statement_ids.join(" + ")} ↔ {match.ledger_ids.join(" + ")} · {match.method} · signed difference ${match.amount_difference} · {match.policy_reference ? `rule ${match.policy_reference} · ` : ""}{match.reason}</li>)}</ul>
      </section>}
      <Lines
        title="Unmatched statement items"
        lines={result.unmatched_statement}
      />
      <Lines title="Unmatched ledger items" lines={result.unmatched_ledger} />
      {exceptions.length > 0 && (
        <section>
          <h3>Investigate these items · {exceptions.length}</h3>
          <p>These are possible leads from imported rows, not confirmed matches or accounting treatments. Exact groups use one cash direction; net split candidates combine a gross row and opposite-sign adjustment. Check the originals and the accountant's rule before confirming either.</p>
          <table>
            <thead><tr><th>Source row</th><th>Possible related rows</th><th>Question</th></tr></thead>
            <tbody>{exceptions.map((item) => (
              <tr key={`${item.side}-${item.id}`}>
                <th>{item.side} {item.id}</th>
                <td>
                  {item.candidate_count === 0 ? "None in imported rows" : `${item.candidate_ids.join(", ")}${item.candidate_count > item.candidate_ids.length ? ` and ${item.candidate_count - item.candidate_ids.length} more` : ""}`}
                  {(item.group_count ?? 0) > 0 && <p>Exact groups: {item.candidate_groups?.map((group) => group.join(" + ")).join("; ")}{(item.group_count ?? 0) > (item.candidate_groups?.length ?? 0) ? ` and ${(item.group_count ?? 0) - (item.candidate_groups?.length ?? 0)} more` : ""}</p>}
                  {(item.split_count ?? 0) > 0 && <p>Net split candidates: {item.split_groups?.map((group) => group.join(" + ")).join("; ")}{(item.split_count ?? 0) > (item.split_groups?.length ?? 0) ? ` and ${(item.split_count ?? 0) - (item.split_groups?.length ?? 0)} more` : ""}</p>}
                </td>
                <td>{item.question}</td>
              </tr>
            ))}</tbody>
          </table>
        </section>
      )}
    </>
  );
}
export default function Prepare() {
  const [params] = useSearchParams();
  const currency = useCurrency(params.get("close"));
  const form = useRef<HTMLFormElement>(null);
  // A bank account's month from the close's current QuickBooks capture, refused unless it ties to the Trial Balance;
  // the capture becomes the ledger source, and the person still reviews and confirms the reconciliation.
  async function fillLedger() {
    const fields = form.current?.elements;
    const account = (fields?.namedItem("account") as HTMLInputElement | null)?.value.trim() ?? "";
    if (!fields || !account) return setError("Enter the bank account's name first.");
    setBusy(true);
    setError("");
    try {
      const ledger: Record<string, string> = await workspaceRequest(`/${params.get("close")}/reconciliation-ledger?account=${encodeURIComponent(account)}`);
      for (const name of ["ledger_csv", "ledger_opening", "ledger_closing", "ledger_reference"]) {
        const field = fields.namedItem(name) as HTMLInputElement | HTMLTextAreaElement | null;
        if (field) field.value = ledger[name];
      }
      const type = fields.namedItem("account_type") as HTMLSelectElement | null;
      if (type) type.value = "bank";
      // The capture itself is the ledger's source; saving reads the ledger from it again.
      setSourceSelections((previous) => ({ ...previous, ledger: { id: ledger.source_id, location: ledger.location } }));
      setResult(undefined);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The captured ledger could not be used.");
    } finally {
      setBusy(false);
    }
  }
  const revision = useRevision(form);
  const heading = useRef<HTMLHeadingElement>(null);
  const [result, setResult] = useState<ReconciliationWorkpaper>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [matchReason, setMatchReason] = useState("");
  const [selection, setSelection] = useState({ statement: "", ledger: "", method: "exact" as ReviewedMatch["method"], reason: "", policy: "", days: "0", amount: "0.00" });
  const [timing, setTiming] = useState({ side: "ledger" as "ledger" | "statement", treatment: "timing" as "timing" | "clearing", clearingRef: "", id: "", amount: "", clearedBy: "", source: "", policy: "", reason: "" });
  const [transfer, setTransfer] = useState({ statement: "", ledger: "", otherStatement: "", otherLedger: "", policy: "", reason: "" });
  const [priorSuggestions, setPriorSuggestions] = useState<{ items: PriorOutstanding[]; message: string; prior_workpaper_id?: string }>();
  useEffect(() => {
    if (result) heading.current?.focus();
  }, [result]);
  const [evidenceFiles, setEvidenceFiles] = useState<EvidenceFile[]>([]);
  const [sourceSelections, setSourceSelections] = useState<
    Record<string, { id: string; location: string }>
  >({});
  useEffect(() => {
    const close = params.get("close");
    if (close)
      workspaceRequest(`/${close}/evidence`)
        .then(setEvidenceFiles)
        .catch((e) => setError(e.message));
  }, [params]);
  async function calculate(input: ReconciliationInput) {
    setBusy(true);
    setError("");
    try {
      const response = await fetch(`${apiBaseUrl}/v1/reconciliations:prepare`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...(await authenticationHeaders()) },
        body: JSON.stringify(input),
      });
      const paper = await response.json();
      if (!response.ok) throw new Error(paper.message ?? "Preparation failed.");
      setResult(paper);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Preparation is unavailable. Your inputs are still here.");
    } finally {
      setBusy(false);
    }
  }
  async function confirmSelection(match: ReviewedMatch) {
    if (!result) return;
    await calculate({ ...result.input, reviewed_matches: [...(result.input.reviewed_matches ?? []), match] });
  }
  async function checkPrior() {
    const close = params.get("close");
    if (!close || !result) return;
    setBusy(true);
    setError("");
    try {
      setPriorSuggestions(await workspaceRequest(`/${close}/reconciliation/prior-items?account=${encodeURIComponent(result.input.account)}&account_type=${encodeURIComponent(result.input.account_type ?? "bank")}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not check the previous close.");
    } finally {
      setBusy(false);
    }
  }
  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const text = (key: string) => String(data.get(key) ?? "");
    const input: ReconciliationInput = {
      entity: text("entity"),
      account: text("account"),
      account_type: text("account_type") as ReconciliationInput["account_type"],
      card_rule_reference: text("card_rule_reference"),
      close_period: text("close_period"),
      currency,
      statement_reference: text("statement_reference"),
      ledger_reference: text("ledger_reference"),
      expected_pages: Number(text("expected_pages")),
      pages: text("pages")
        .split(",")
        .filter((v) => v.trim() !== "")
        .map(Number),
      statement_opening: text("statement_opening"),
      statement_closing: text("statement_closing"),
      ledger_opening: text("ledger_opening"),
      ledger_closing: text("ledger_closing"),
      statement_csv: text("statement_csv"),
      ledger_csv: text("ledger_csv"),
      policy_confirmed: data.get("policy_confirmed") === "on",
      reviewed_matches: [],
      prior_outstanding: [],
      current_outstanding: [],
      transfers: [],
      other_account: text("other_account"),
      other_statement_reference: text("other_statement_reference"),
      other_ledger_reference: text("other_ledger_reference"),
      other_statement_csv: text("other_statement_csv"),
      other_ledger_csv: text("other_ledger_csv"),
      native_status: text("native_status") as ReconciliationInput["native_status"],
      native_status_reference: text("native_status_reference"),
    };
    setResult(undefined);
    setPriorSuggestions(undefined);
    await calculate(input);
  }
  function example(card = false) {
    const values = {
      entity: params.get("entity") ?? "Acme Services (example)",
      account: card ? "Corporate card" : "Operating bank",
      account_type: card ? "credit_card" : "bank",
      card_rule_reference: card ? "Client accountant approved card liability rule (example)" : "",
      close_period: params.get("period") ?? "2026-09",
      statement_reference: "Synthetic statement, pages 1–2",
      ledger_reference: "Synthetic ledger export",
      expected_pages: "2",
      pages: "1,2",
      statement_opening: card ? "0.00" : "1000.00",
      statement_closing: card ? "75.00" : "1150.00",
      ledger_opening: card ? "0.00" : "1000.00",
      ledger_closing: card ? "85.00" : "1100.00",
      statement_csv: card ? `id,date,amount\nS1,${params.get("period") ?? "2026-09"}-02,100.00\nS2,${params.get("period") ?? "2026-09"}-05,-25.00` : `id,date,amount\nS1,${params.get("period") ?? "2026-09"}-02,200.00\nS2,${params.get("period") ?? "2026-09"}-05,-50.00`,
      ledger_csv: card ? `id,date,amount\nL1,${params.get("period") ?? "2026-09"}-02,100.00\nL2,${params.get("period") ?? "2026-09"}-05,-25.00\nL3,${params.get("period") ?? "2026-09"}-28,10.00` : `id,date,amount\nL1,${params.get("period") ?? "2026-09"}-02,200.00\nL2,${params.get("period") ?? "2026-09"}-05,-50.00\nL3,${params.get("period") ?? "2026-09"}-28,-50.00`,
    };
    for (const [name, value] of Object.entries(values)) {
      (form.current!.elements.namedItem(name) as HTMLInputElement).value =
        value;
    }
    (
      form.current!.elements.namedItem("policy_confirmed") as HTMLInputElement
    ).checked = false;
    setResult(undefined);
    setPriorSuggestions(undefined);
    setError("");
  }
  return (
    <>
      <PageHeader icon="reconcile" title="Statement reconciliation">
        Import one full month of a {currency} bank or credit-card account. Bank deposits increase cash; card charges increase the liability. Bank
        withdrawals, card payments and card credits decrease it. Normalize both CSVs to the selected sign convention before review.
      </PageHeader>
      <details open={!result}>
        <summary>
          {result
            ? "Edit source details and prepare again"
            : "Statement and ledger details"}
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
            setPriorSuggestions(undefined);
            setError("");
          }}
        >
          <fieldset disabled={busy || !revision.ready}>
            <legend>Source context</legend>
            {params.get("close") && (
              <div>
                <p>
                  Load uploaded CSV evidence directly into this form, then
                  confirm its balances and page inventory.
                </p>
                {["statement", "ledger", "other_statement", "other_ledger"].map((role) => (
                  <label key={role}>
                    {role} evidence
                    <select
                      value={sourceSelections[role]?.id ?? ""}
                      onChange={async (e) => {
                        const id = e.target.value;
                        if (!id) return;
                        try {
                          const source = evidenceFiles.find(
                            (f) => f.id === id,
                          )!;
                          const blob = await workspaceRequest(
                            `/${params.get("close")}/evidence/${id}`,
                            undefined,
                            true,
                          );
                          const text = await blob.text();
                          (
                            form.current!.elements.namedItem(
                              `${role}_csv`,
                            ) as HTMLTextAreaElement
                          ).value = text;
                          (
                            form.current!.elements.namedItem(
                              `${role}_reference`,
                            ) as HTMLInputElement
                          ).value = source.name;
                          setSourceSelections((previous) => ({
                            ...previous,
                            [role]: { id, location: "Entire uploaded CSV" },
                          }));
                          setResult(undefined);
                        } catch (e) {
                          setError(
                            e instanceof Error
                              ? e.message
                              : "Could not load evidence.",
                          );
                        }
                      }}
                    >
                      <option value="">Choose uploaded CSV</option>
                      {evidenceFiles
                        .filter(
                          (f) =>
                            !f.superseded_by &&
                            (f.name.toLowerCase().endsWith(".csv") || f.id === sourceSelections[role]?.id),
                        )
                        .map((f) => (
                          <option key={f.id} value={f.id}>
                            {f.name} · #{f.id}
                          </option>
                        ))}
                    </select>
                  </label>
                ))}
              </div>
            )}

            <button type="button" onClick={() => example(false)}>Use bank example</button>
            <button type="button" onClick={() => example(true)}>Use card example</button>
            <div className="form-grid">
              <label>
                Client / entity
                <input
                  name="entity"
                  required
                  maxLength={200}
                  defaultValue={params.get("entity") ?? ""}
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
                Account
                <input name="account" required maxLength={200} />
              </label>
              <label>Account type<select name="account_type" defaultValue="bank"><option value="bank">Bank cash</option><option value="credit_card">Credit-card liability</option></select></label>
              <label>Client accountant's approved card sign and clearing rule<input name="card_rule_reference" maxLength={200} placeholder="Required for credit cards; cite the approved procedure" /></label>
              <label>
                Statement reference
                <input name="statement_reference" required maxLength={200} />
              </label>
              <label>
                Ledger export reference
                <input name="ledger_reference" required maxLength={200} />
              </label>
              <label>
                Other account (for reviewed transfers)
                <input name="other_account" maxLength={200} />
              </label>
              <label>
                Other account statement reference
                <input name="other_statement_reference" maxLength={200} />
              </label>
              <label>
                Other account ledger reference
                <input name="other_ledger_reference" maxLength={200} />
              </label>
              <label>
                Expected statement pages
                <input
                  name="expected_pages"
                  type="number"
                  min={1}
                  max={1000}
                  required
                />
              </label>
              <label>
                Pages received
                <input name="pages" placeholder="1,2,3" />
                <small>
                  Comma-separated page numbers; missing pages remain blockers.
                </small>
              </label>
              {(
                [
                  ["statement_opening", "Statement opening balance"],
                  ["statement_closing", "Statement closing balance"],
                  ["ledger_opening", "Ledger opening balance"],
                  ["ledger_closing", "Ledger closing balance"],
                ] as const
              ).map(([name, label]) => (
                <label key={name}>
                  {label} ({currency})
                  <input
                    name={name}
                    inputMode="decimal"
                    required
                    maxLength={14}
                  />
                </label>
              ))}
            </div>
          </fieldset>
          <fieldset disabled={busy || !revision.ready}>
            <legend>Transaction detail</legend>
            <p>
              Paste CSV with header <code>id,date,amount</code>. Use unique
              source IDs, YYYY-MM-DD dates and signed amounts. Up to 2,000 rows
              per source. A header-only CSV means no movement.
            </p>
            <div className="form-grid">
              <label>
                Independent statement CSV
                <textarea
                  name="statement_csv"
                  required
                  rows={8}
                  maxLength={200000}
                />
              </label>
              <label>
                Ledger CSV
                <textarea
                  name="ledger_csv"
                  required
                  rows={8}
                  maxLength={200000}
                />
              </label>
              {params.get("close") && (
                <button type="button" disabled={busy} onClick={() => void fillLedger()}>
                  Fill the ledger from the QuickBooks capture
                </button>
              )}
              <label>Other account statement CSV (transfers only)<textarea name="other_statement_csv" rows={5} maxLength={200000} /></label>
              <label>Other account ledger CSV (transfers only)<textarea name="other_ledger_csv" rows={5} maxLength={200000} /></label>
            </div>
            <label>QuickBooks status observed by reviewer
              <select name="native_status" defaultValue="unverified"><option value="unverified">Not verified</option><option value="reconciled">Reconciled (cite observation)</option><option value="unreconciled">Not reconciled (cite observation)</option></select>
            </label>
            <label>QuickBooks status source reference<input name="native_status_reference" maxLength={200} placeholder="Account reconciliation report and date" /></label>
            <p>A saved source-linked workpaper also requires the QuickBooks report as separate PDF or text evidence. The app does not read or change QuickBooks' native reconciliation flag.</p>
            <label className="checkbox-label">
              <input name="policy_confirmed" type="checkbox" required />
              <span>I confirm the same entity, account and full {currency} month on both sources. Bank deposits are positive and withdrawals negative; card charges are positive and payments or credits negative. The selected method and exact match candidates need accountant review.</span>
            </label>
          </fieldset>
          {error && <p role="alert">{error}</p>}
          <button className="primary" disabled={busy || !revision.ready}>
            {busy ? "Preparing…" : "Prepare reconciliation"}
          </button>
        </form>
      </details>
      {result && (
        <section>
          <h2 ref={heading} tabIndex={-1}>
            Prepared reconciliation draft
          </h2>
          <ReconciliationResult result={result} />
          <details>
            <summary>Confirm matches after inspecting the originals</summary>
            <p>These choices create a revised draft calculation. They do not approve the workpaper or mark QuickBooks reconciled. Groups and net splits need an accountant-approved rule. A nonzero amount difference remains a blocker.</p>
            <label>What did you verify in the original records?<input value={matchReason} onChange={(e) => setMatchReason(e.target.value)} minLength={5} maxLength={500} placeholder="Describe the matching evidence" /></label>
            <label>Approved rule for grouped or net split matches<input value={selection.policy} maxLength={200} onChange={(e) => setSelection({ ...selection, policy: e.target.value })} placeholder="Client accountant's approved rule" /></label>
            {result.matches.map((match) => <button key={`${match.statement_id}-${match.ledger_id}`} disabled={busy || matchReason.trim().length < 5} onClick={() => void confirmSelection({ statement_ids: [match.statement_id], ledger_ids: [match.ledger_id], method: "exact", reason: matchReason })}>Confirm {match.statement_id} ↔ {match.ledger_id}</button>)}
            {(result.exceptions ?? []).flatMap((item) => (item.candidate_groups ?? []).map((group) => {
              const match: ReviewedMatch = item.side === "statement"
                ? { statement_ids: [item.id], ledger_ids: group, method: "exact", reason: matchReason, policy_reference: selection.policy }
                : { statement_ids: group, ledger_ids: [item.id], method: "exact", reason: matchReason, policy_reference: selection.policy };
              return <button key={`${item.side}-${item.id}-${group.join("-")}`} disabled={busy || matchReason.trim().length < 5 || selection.policy.trim().length < 5} onClick={() => void confirmSelection(match)}>Confirm group {match.statement_ids.join(" + ")} ↔ {match.ledger_ids.join(" + ")}</button>;
            }))}
            {(result.exceptions ?? []).flatMap((item) => (item.split_groups ?? []).map((group) => {
              const match: ReviewedMatch = item.side === "statement"
                ? { statement_ids: [item.id], ledger_ids: group, method: "split", reason: matchReason, policy_reference: selection.policy }
                : { statement_ids: group, ledger_ids: [item.id], method: "split", reason: matchReason, policy_reference: selection.policy };
              return <button key={`split-${item.side}-${item.id}-${group.join("-")}`} disabled={busy || matchReason.trim().length < 5 || selection.policy.trim().length < 5} onClick={() => void confirmSelection(match)}>Confirm net split {match.statement_ids.join(" + ")} ↔ {match.ledger_ids.join(" + ")}</button>;
            }))}
            <form onSubmit={(event) => {
              event.preventDefault();
              const days = Number(selection.days);
              const statement_ids = selection.statement.split(",").map((s) => s.trim());
              const ledger_ids = selection.ledger.split(",").map((s) => s.trim());
              const grouped = statement_ids.length > 1 || ledger_ids.length > 1;
              void confirmSelection({ statement_ids, ledger_ids, method: selection.method, reason: selection.reason, ...(selection.method !== "exact" || grouped ? { policy_reference: selection.policy } : {}), ...(selection.method !== "exact" ? { date_window_days: days } : {}), ...(selection.method === "tolerance" ? { amount_tolerance: selection.amount } : {}) });
            }}>
              <fieldset disabled={busy}>
                <legend>Review another match or net split</legend>
                <p>Enter IDs shown above, up to five on each side. Grouped, split and tolerance methods require the accountant's approved rule.</p>
                <label>Statement IDs, comma-separated<input required value={selection.statement} onChange={(e) => setSelection({ ...selection, statement: e.target.value })} /></label>
                <label>Ledger IDs, comma-separated<input required value={selection.ledger} onChange={(e) => setSelection({ ...selection, ledger: e.target.value })} /></label>
                <label>Reviewed method<select value={selection.method} onChange={(e) => setSelection({ ...selection, method: e.target.value as ReviewedMatch["method"] })}><option value="exact">Exact</option><option value="split">Net split settlement</option><option value="tolerance">Approved tolerance</option></select></label>
                {selection.method !== "exact" && <label>Date window, days<input type="number" min="0" max="31" value={selection.days} onChange={(e) => setSelection({ ...selection, days: e.target.value })} /></label>}
                {selection.method === "tolerance" && <label>Maximum amount difference, {currency}<input inputMode="decimal" value={selection.amount} onChange={(e) => setSelection({ ...selection, amount: e.target.value })} /></label>}
                <label>Why do these original records belong together?<textarea required minLength={5} maxLength={500} value={selection.reason} onChange={(e) => setSelection({ ...selection, reason: e.target.value })} /></label>
                <button>Confirm reviewed group and recalculate</button>
              </fieldset>
            </form>
          </details>
          <details>
            <summary>Resolve timing, clearing and transfers with approved rules</summary>
            <p>Inspect the original records and cite the accountant-approved method. Each choice recalculates the bridge; an unexplained difference stays blocked. Upload prior-period support and both other-account CSVs before saving source-linked instructions.</p>
            {params.get("close") && <button disabled={busy} onClick={() => void checkPrior()}>Check previous approved close for outstanding items</button>}
            {priorSuggestions && <section role="status">
              <p>{priorSuggestions.message}{priorSuggestions.prior_workpaper_id ? ` Prior workpaper #${priorSuggestions.prior_workpaper_id}.` : ""}</p>
              {priorSuggestions.items.length > 0 && <>
                <ul>{priorSuggestions.items.map((item) => <li key={`${item.side}-${item.id}`}>{item.side} {item.id} · ${item.amount} · {item.source_reference}</li>)}</ul>
                <button disabled={busy} onClick={() => void calculate({ ...result.input, prior_outstanding: [...(result.input.prior_outstanding ?? []), ...priorSuggestions.items.filter((item) => !(result.input.prior_outstanding ?? []).some((old) => old.side === item.side && old.id === item.id))] })}>Use these as draft prior items</button>
              </>}
            </section>}
            {result.input.prior_outstanding?.map((item, index) => <button key={`remove-prior-${index}`} disabled={busy} onClick={() => void calculate({ ...result.input, prior_outstanding: result.input.prior_outstanding?.filter((_, i) => i !== index) })}>Remove prior item {item.id}</button>)}
            {result.input.current_outstanding?.map((item, index) => <button key={`remove-current-${index}`} disabled={busy} onClick={() => void calculate({ ...result.input, current_outstanding: result.input.current_outstanding?.filter((_, i) => i !== index) })}>Remove current item {item.id}</button>)}
            {result.input.transfers?.map((item, index) => <button key={`remove-transfer-${index}`} disabled={busy} onClick={() => void calculate({ ...result.input, transfers: result.input.transfers?.filter((_, i) => i !== index) })}>Remove transfer {item.statement_id}</button>)}
            <label>Source side
              <select value={timing.side} onChange={(e) => setTiming({ ...timing, side: e.target.value as "statement" | "ledger" })}><option value="ledger">Ledger</option><option value="statement">Statement</option></select>
            </label>
            <label>Reviewed treatment<select value={timing.treatment} onChange={(e) => setTiming({ ...timing, treatment: e.target.value as "timing" | "clearing", clearingRef: "" })}><option value="timing">Timing difference</option><option value="clearing">Clearing account item</option></select></label>
            {timing.treatment === "clearing" && <label>Clearing-account source reference<input maxLength={200} value={timing.clearingRef} onChange={(e) => setTiming({ ...timing, clearingRef: e.target.value })} placeholder="Source and account showing the unsettled item" /></label>}
            <label>Source row ID<input value={timing.id} onChange={(e) => setTiming({ ...timing, id: e.target.value })} placeholder="Current row ID or prior item ID" /></label>
            <label>Prior item signed amount ({currency})<input inputMode="decimal" value={timing.amount} onChange={(e) => setTiming({ ...timing, amount: e.target.value })} /></label>
            <label>Current-period clearing row ID on opposite source (optional)<input value={timing.clearedBy} onChange={(e) => setTiming({ ...timing, clearedBy: e.target.value })} /></label>
            <label>Prior workpaper or statement citation<input maxLength={200} value={timing.source} onChange={(e) => setTiming({ ...timing, source: e.target.value })} /></label>
            <label>Accountant-approved rule reference<input maxLength={200} value={timing.policy} onChange={(e) => setTiming({ ...timing, policy: e.target.value })} /></label>
            <label>What did you verify?<textarea minLength={5} maxLength={500} value={timing.reason} onChange={(e) => setTiming({ ...timing, reason: e.target.value })} /></label>
            <div>
              <button disabled={busy || !timing.id || !timing.amount || timing.source.trim().length < 5 || timing.policy.trim().length < 5 || timing.reason.trim().length < 5 || (timing.treatment === "clearing" && timing.clearingRef.trim().length < 5)} onClick={() => void calculate({ ...result.input, prior_outstanding: [...(result.input.prior_outstanding ?? []), { side: timing.side, id: timing.id, amount: timing.amount, treatment: timing.treatment, clearing_reference: timing.clearingRef, cleared_by: timing.clearedBy, source_reference: timing.source, policy_reference: timing.policy, reason: timing.reason }] })}>Add cited prior item</button>
              <button disabled={busy || !timing.id || timing.policy.trim().length < 5 || timing.reason.trim().length < 5 || (timing.treatment === "clearing" && timing.clearingRef.trim().length < 5)} onClick={() => void calculate({ ...result.input, current_outstanding: [...(result.input.current_outstanding ?? []), { side: timing.side, id: timing.id, treatment: timing.treatment, clearing_reference: timing.clearingRef, policy_reference: timing.policy, reason: timing.reason }] })}>Classify current unmatched row</button>
            </div>
            <h3>Pair a transfer across both accounts</h3>
            <p>Enter all four row IDs from the current and other account statement/ledger CSVs. Their signed amounts must agree in each account and offset between accounts.</p>
            <label>Current account statement row<input value={transfer.statement} onChange={(e) => setTransfer({ ...transfer, statement: e.target.value })} /></label>
            <label>Current account ledger row<input value={transfer.ledger} onChange={(e) => setTransfer({ ...transfer, ledger: e.target.value })} /></label>
            <label>Other account statement row<input value={transfer.otherStatement} onChange={(e) => setTransfer({ ...transfer, otherStatement: e.target.value })} /></label>
            <label>Other account ledger row<input value={transfer.otherLedger} onChange={(e) => setTransfer({ ...transfer, otherLedger: e.target.value })} /></label>
            <label>Accountant-approved transfer rule<input maxLength={200} value={transfer.policy} onChange={(e) => setTransfer({ ...transfer, policy: e.target.value })} /></label>
            <label>What did you verify?<textarea minLength={5} maxLength={500} value={transfer.reason} onChange={(e) => setTransfer({ ...transfer, reason: e.target.value })} /></label>
            <button disabled={busy || !transfer.statement || !transfer.ledger || !transfer.otherStatement || !transfer.otherLedger || transfer.policy.trim().length < 5 || transfer.reason.trim().length < 5} onClick={() => void calculate({ ...result.input, transfers: [...(result.input.transfers ?? []), { statement_id: transfer.statement, ledger_id: transfer.ledger, other_statement_id: transfer.otherStatement, other_ledger_id: transfer.otherLedger, policy_reference: transfer.policy, reason: transfer.reason }] })}>Confirm transfer pair and recalculate</button>
          </details>
          <button
            onClick={() => {
              const url = URL.createObjectURL(
                new Blob([JSON.stringify(result, null, 2)], {
                  type: "application/json",
                }),
              );
              const link = document.createElement("a");
              link.href = url;
              link.download = "reconciliation-workpaper.json";
              link.click();
              setTimeout(() => URL.revokeObjectURL(url), 0);
            }}
          >
            Export workpaper JSON
          </button>
          <SaveDraft
            kind="reconciliation"
            input={result.input}
            initialSources={{
              ...Object.fromEntries(revision.sources.map((s) => [s.role, s])),
              ...sourceSelections,
            }}
          />
        </section>
      )}
    </>
  );
}
