import { useRevision } from "../closes/useRevision";
import { SourceReview } from "../closes/Evidence";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { authenticationHeaders } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
import { workspaceRequest } from "../closes/workspace";
import type { EvidenceFile } from "../closes/Evidence";
import { SaveDraft } from "../closes/SaveDraft";
import type {
  ReconciliationInput,
  ReconciliationWorkpaper,
  ReconciliationLine,
} from "../../../../../packages/contracts/reconciliation";

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
              <th>Signed USD</th>
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
  return (
    <>
      <p className="note">
        Draft workpaper only. Exact matches are candidates, not confirmed
        matches or native bank reconciliation. Source references and page
        inventory are user-supplied; documents have not been independently
        inspected.
      </p>
      <p>
        {result.input.entity} · {result.input.account} ·{" "}
        {result.input.close_period} · USD
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
      </dl>
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
      <Lines
        title="Unmatched statement items"
        lines={result.unmatched_statement}
      />
      <Lines title="Unmatched ledger items" lines={result.unmatched_ledger} />
    </>
  );
}
export default function Prepare() {
  const [params] = useSearchParams();
  const form = useRef<HTMLFormElement>(null);
  const revision = useRevision(form);
  const heading = useRef<HTMLHeadingElement>(null);
  const [result, setResult] = useState<ReconciliationWorkpaper>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
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
  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const text = (key: string) => String(data.get(key) ?? "");
    const input: ReconciliationInput = {
      entity: text("entity"),
      account: text("account"),
      close_period: text("close_period"),
      currency: "USD",
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
    };
    setBusy(true);
    setError("");
    setResult(undefined);
    try {
      const response = await fetch(`${apiBaseUrl}/v1/reconciliations:prepare`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          ...(await authenticationHeaders()),
        },
        body: JSON.stringify(input),
      });
      const paper = await response.json();
      if (!response.ok) throw new Error(paper.message ?? "Preparation failed.");
      setResult(paper);
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Preparation is unavailable. Your inputs are still here.",
      );
    } finally {
      setBusy(false);
    }
  }
  function example() {
    const values = {
      entity: params.get("entity") ?? "Acme Services (example)",
      account: "Operating bank",
      close_period: params.get("period") ?? "2026-09",
      statement_reference: "Synthetic statement, pages 1–2",
      ledger_reference: "Synthetic ledger export",
      expected_pages: "2",
      pages: "1,2",
      statement_opening: "1000.00",
      statement_closing: "1150.00",
      ledger_opening: "1000.00",
      ledger_closing: "1100.00",
      statement_csv: `id,date,amount\nS1,${params.get("period") ?? "2026-09"}-02,200.00\nS2,${params.get("period") ?? "2026-09"}-05,-50.00`,
      ledger_csv: `id,date,amount\nL1,${params.get("period") ?? "2026-09"}-02,200.00\nL2,${params.get("period") ?? "2026-09"}-05,-50.00\nL3,${params.get("period") ?? "2026-09"}-28,-50.00`,
    };
    for (const [name, value] of Object.entries(values)) {
      (form.current!.elements.namedItem(name) as HTMLInputElement).value =
        value;
    }
    (
      form.current!.elements.namedItem("policy_confirmed") as HTMLInputElement
    ).checked = false;
    setResult(undefined);
    setError("");
  }
  return (
    <>
      <p className="eyebrow">STATEMENT RECONCILIATION / PREPARATION</p>
      <h1>See what ties. Investigate what doesn’t.</h1>
      <p>
        Import one full month of a USD bank account. Deposits are positive;
        withdrawals are negative on both sources. Credit-card accounts, grouped
        matches, date tolerances, and prior-period outstanding items need a
        separate method.
      </p>
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
                {["statement", "ledger"].map((role) => (
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
                            f.name.toLowerCase().endsWith(".csv"),
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

            <button type="button" onClick={example}>
              Use an example with an outstanding item
            </button>
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
                Bank account
                <input name="account" required maxLength={200} />
              </label>
              <label>
                Statement reference
                <input name="statement_reference" required maxLength={200} />
              </label>
              <label>
                Ledger export reference
                <input name="ledger_reference" required maxLength={200} />
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
                  {label} (USD)
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
            </div>
            <label className="checkbox-label">
              <input name="policy_confirmed" type="checkbox" required />
              <span>
                I confirm these are the same entity and USD bank account,
                balances cover the first through last day of the selected month,
                both sources use positive cash inflows and negative outflows,
                and unique exact date/amount matching is suitable for preparing
                candidates.
              </span>
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
