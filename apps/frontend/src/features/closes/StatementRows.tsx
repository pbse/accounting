import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type ProposedRow = { page: number; date: string; description: string; amount: string; quote: string; issue?: string };
type Review = {
  extraction: null | {
    id: string;
    current: boolean;
    confirmed_at: string;
    produced_id: string;
    proposal: { rows: ProposedRow[]; account: string; period_start: string; period_end: string; opening_balance: string; closing_balance: string; currency: string; issues: string[] };
  };
  checks: { pages: number; page_problems: string[]; page_warnings: string[] };
};
type Row = { index: number; page: number; date: string; description: string; amount: string; checked: boolean; quote: string; issue: string };

const cents = (amount: string) => (/^-?(0|[1-9]\d{0,9})\.\d{2}$/.test(amount) ? Math.round(Number(amount) * 100) : NaN);

// One PDF statement's proposed rows, checked page by page against the original before they become CSV evidence.
export function StatementRows({ closeId, evidenceId, onConfirmed }: { closeId: string; evidenceId: string; onConfirmed: () => void }) {
  const base = `/${closeId}/evidence/${evidenceId}/statement`;
  const [review, setReview] = useState<Review>();
  const [rows, setRows] = useState<Row[]>([]);
  const [removed, setRemoved] = useState<number[]>([]);
  const [pages, setPages] = useState<number[]>([]);
  const [fields, setFields] = useState({ account: "", period_start: "", period_end: "", opening_balance: "", closing_balance: "", currency: "" });
  const [fieldsChecked, setFieldsChecked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  function show(next: Review) {
    setReview(next);
    const p = next.extraction?.proposal;
    if (!p) return;
    setRows(p.rows.map((r, index) => ({ index, page: r.page, date: r.date, description: r.description, amount: r.amount, checked: false, quote: r.quote, issue: r.issue ?? "" })));
    setRemoved([]);
    setPages([]);
    setFields({ account: p.account, period_start: p.period_start, period_end: p.period_end, opening_balance: p.opening_balance, closing_balance: p.closing_balance, currency: p.currency });
  }
  useEffect(() => {
    workspaceRequest(base).then(show).catch((e) => setMessage(e instanceof Error ? e.message : "Statement review unavailable."));
  }, [base]);
  async function run(path: string, body: unknown) {
    setBusy(true);
    setMessage("");
    try {
      const result = await workspaceRequest(path, body);
      if (path === base) show(result);
      else {
        setMessage(`Confirmed as evidence #${result.evidence_id}. Its preparation job continues in Work.`);
        onConfirmed();
        show(await workspaceRequest(base));
      }
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The statement could not be read.");
    } finally {
      setBusy(false);
    }
  }
  const set = (n: number, change: Partial<Row>) => setRows((all) => all.map((r, i) => (i === n ? { ...r, ...change, checked: change.checked ?? false } : r)));
  const extraction = review?.extraction;
  const total = rows.reduce((sum, r) => sum + cents(r.amount), cents(fields.opening_balance)) - cents(fields.closing_balance);
  const difference = Number.isNaN(total) ? "unreadable" : (total / 100).toFixed(2);
  const pageCount = review?.checks.pages ?? 0;
  const proposed = extraction?.proposal.rows ?? [];
  const needsCheck = (r: Row) => r.index === -1 || !!r.issue || proposed[r.index]?.page !== r.page || proposed[r.index]?.date !== r.date || proposed[r.index]?.amount !== r.amount || proposed[r.index]?.description !== r.description;
  const p = extraction?.proposal;
  const fieldsChanged = !!p && (fields.account !== p.account || fields.period_start !== p.period_start || fields.period_end !== p.period_end || fields.opening_balance !== p.opening_balance || fields.closing_balance !== p.closing_balance || fields.currency.toUpperCase() !== p.currency.toUpperCase());
  const unchecked = rows.filter((r) => needsCheck(r) && !r.checked).length;
  const ready = !!extraction && !extraction.confirmed_at && extraction.current && review.checks.page_problems.length === 0 && pages.length === pageCount && difference === "0.00" && unchecked === 0 && (!fieldsChanged || fieldsChecked);
  return (
    <details>
      <summary>Statement rows</summary>
      {!extraction || !extraction.current ? (
        <>
          <p>Propose this statement's transaction rows with page references, then check every page against the original before they become a statement CSV.</p>
          <button type="button" disabled={busy} onClick={() => void run(base, {})}>
            {busy ? "Reading the statement…" : "Propose rows"}
          </button>
        </>
      ) : extraction.confirmed_at ? (
        <p>Confirmed {new Date(extraction.confirmed_at).toLocaleString()} as evidence #{extraction.produced_id}.</p>
      ) : (
        <>
          {review.checks.page_problems.map((p) => (
            <p key={p} role="alert">{p} Resolve this before confirming.</p>
          ))}
          {review.checks.page_warnings.map((p) => (
            <p key={p}>{p}</p>
          ))}
          {extraction.proposal.issues.map((p) => (
            <p key={p} role="alert">{p}</p>
          ))}
          <div className="stack">
            {(["account", "period_start", "period_end", "opening_balance", "closing_balance", "currency"] as const).map((k) => (
              <label key={k}>
                {k.replaceAll("_", " ")}
                <input value={fields[k]} type={k.startsWith("period") ? "date" : "text"} onChange={(e) => setFields({ ...fields, [k]: e.target.value })} />
              </label>
            ))}
          </div>
          {fieldsChanged && (
            <label>
              <input type="checkbox" checked={fieldsChecked} onChange={(e) => setFieldsChecked(e.target.checked)} /> I checked the account, period, balances and currency against the original.
            </label>
          )}
          <table>
            <thead>
              <tr><th>Page</th><th>Date</th><th>Description</th><th>Amount</th><th>Checked</th><th /></tr>
            </thead>
            <tbody>
              {rows.map((r, n) => (
                <tr key={n} className={r.issue ? "warn" : undefined} title={r.quote}>
                  <td><input type="number" min={1} max={pageCount} value={r.page} onChange={(e) => set(n, { page: Number(e.target.value) })} /></td>
                  <td><input type="date" value={r.date} onChange={(e) => set(n, { date: e.target.value })} /></td>
                  <td><input value={r.description} maxLength={300} onChange={(e) => set(n, { description: e.target.value })} />{r.quote && <small className="block">“{r.quote}”</small>}</td>
                  <td><input value={r.amount} inputMode="decimal" onChange={(e) => set(n, { amount: e.target.value })} />{r.issue && <small role="alert"> {r.issue}</small>}</td>
                  <td><input type="checkbox" aria-label={`Row ${n + 1} checked against the original`} checked={r.checked} onChange={(e) => set(n, { checked: e.target.checked })} /></td>
                  <td>
                    <button type="button" onClick={() => { if (r.index >= 0) setRemoved((all) => [...all, r.index]); setRows((all) => all.filter((_, i) => i !== n)); }}>Remove</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <button type="button" onClick={() => setRows((all) => [...all, { index: -1, page: 1, date: fields.period_start, description: "", amount: "", checked: false, quote: "", issue: "Added by you; check it against the original." }])}>Add a row</button>
          <p role="status">Opening balance plus the rows minus the closing balance: {difference}. {unchecked > 0 && `${unchecked} added, changed or flagged row${unchecked === 1 ? "" : "s"} to check.`}</p>
          <fieldset>
            <legend>Pages checked against the original, including every row's direction</legend>
            {Array.from({ length: pageCount }, (_, i) => i + 1).map((p) => (
              <label key={p}>
                <input type="checkbox" checked={pages.includes(p)} onChange={(e) => setPages((all) => (e.target.checked ? [...all, p] : all.filter((x) => x !== p)))} /> Page {p}
              </label>
            ))}
          </fieldset>
          <button type="button" className="primary" disabled={busy || !ready} onClick={() => void run(`${base}/confirm`, { extraction_id: extraction.id, rows: rows.map(({ index, page, date, description, amount, checked }) => ({ index, page, date, description, amount, checked })), removed, pages_checked: pages, ...fields, fields_checked: fieldsChecked, confirmed: true })}>
            Confirm rows as statement CSV
          </button>
        </>
      )}
      {message && <p role="status">{message}</p>}
    </details>
  );
}
