import { type FormEvent, useEffect, useState } from "react";
import { amount, workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Line = { account: string; account_id: string; months: Record<string, string> };
type Version = {
  id: string;
  name: string;
  company: string;
  currency: string;
  lines: Line[];
  source: string;
  created_by: string;
  created_at: string;
  approved_by: string;
  approved_at: string | null;
  problems: string[];
  totals: Record<string, { income: string; expense: string; net: string; unmapped: string }>;
};
type Figure = { actual: string; budget: string; variance: string; percent: string | null; favorable: boolean; material: boolean };
type Row = { account_id: string; name: string; class: "Revenue" | "Expense"; month: Figure; ytd: Figure; prior_month: string | null; unbudgeted: boolean };
type Report = {
  state: "no_budget" | "waiting" | "ready";
  reason?: string;
  period: string;
  versions: Version[];
  accounts: { id: string; name: string; class: string }[];
  baseline?: { id: string; name: string; approved_by: string; approved_at: string };
  rows?: Row[];
  totals?: Record<"income_month" | "income_ytd" | "expense_month" | "expense_ytd" | "net_month" | "net_ytd", Figure>;
  currency?: string;
  ytd_from?: string;
  ytd_missing?: string[];
  ytd_unbudgeted?: string[];
  prior_month?: string;
  unmapped?: string[];
  materiality?: string;
  note?: string;
};

// Budget against actual for the close month and the fiscal year to date, from an approved baseline the firm imported.
// readOnly is an executive's view: the comparison alone, without budget versions to import or approve.
export function Budget({ closeId, readOnly = false }: { closeId: string; readOnly?: boolean }) {
  const [report, setReport] = useState<Report>();
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/budget`)
      .then((data) => live && (setReport(data), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "The budget comparison is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, version]);
  async function act(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      const result = await workspaceRequest(`/${closeId}/budget`, body);
      setSaved(result.problems?.length ? `${done} ${result.problems.length} line(s) still need a ledger account.` : done);
      setVersion((n) => n + 1);
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : "The budget could not be saved.");
      return false;
    } finally {
      setBusy(false);
    }
  }
  async function importBudget(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    const file = form.get("file");
    const csv = file instanceof File && file.size > 0 ? await file.text() : String(form.get("csv") ?? "");
    if (await act({ action: "import", name: form.get("name"), csv, source: file instanceof File && file.size > 0 ? file.name : "Pasted CSV" }, "Imported as a draft.")) element.reset();
  }
  if (!report) return error ? <p role="alert">{error}</p> : null;
  const show = (value: string | null | undefined) => (value == null ? "–" : amount(value, report.currency));
  const names: Record<string, string> = Object.fromEntries(report.accounts.map((a) => [a.id, a.name]));
  const variance = (f: Figure) => (
    <td style={{ whiteSpace: "nowrap", color: f.variance === "0.00" ? undefined : f.favorable ? "var(--ok, #067647)" : "var(--danger, #b42318)" }}>
      {show(f.variance)}
      {f.percent !== null && <small> ({f.percent}%)</small>}
      <small> {f.variance === "0.00" ? "" : f.favorable ? "favorable" : "unfavorable"}</small>
      {f.material && <span className="status warn"> Material</span>}
    </td>
  );
  const totalRow = (label: string, month: Figure, ytd: Figure) => (
    <tr key={label}>
      <th scope="row" style={{ textAlign: "left" }}>
        {label}
      </th>
      <td>{show(month.actual)}</td>
      <td>{show(month.budget)}</td>
      {variance(month)}
      <td>{show(ytd.actual)}</td>
      <td>{show(ytd.budget)}</td>
      {variance(ytd)}
      <td />
    </tr>
  );
  return (
    <section aria-label="Budget against actual">
      <SectionHeader title="Budget against actual" />
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">{saved}</p>}
      {report.state === "no_budget" && <p className="empty">No approved budget covers {report.period}.{readOnly ? "" : " Import the client's agreed budget below; nothing is assumed until a person approves it."}</p>}
      {report.state === "waiting" && <p className="empty">{report.reason}</p>}
      {report.state === "ready" && report.rows && report.totals && (
        <>
          <p className="note">
            Approved baseline “{report.baseline!.name}” (approved by {report.baseline!.approved_by}). Year to date from {report.ytd_from}; prior month {report.prior_month}.{" "}
            {report.materiality ? `Material means a difference of at least ${show(report.materiality)}, the firm's materiality.` : "The firm has no materiality amount, so nothing is flagged material."}{" "}
            {report.note}
          </p>
          {report.ytd_missing!.length > 0 && (
            <p role="status">No books for {report.ytd_missing!.join(", ")}: year to date compares only the months that have books, on both sides.</p>
          )}
          {report.ytd_unbudgeted!.length > 0 && <p role="status">The budget has no column for {report.ytd_unbudgeted!.join(", ")}: those months are left out of year to date on both sides.</p>}
          {report.unmapped!.length > 0 && (
            <p role="status">Budget lines without a current income or expense account are left out: {report.unmapped!.join(", ")}. Remap them in a new version.</p>
          )}
          <table>
            <caption>{report.period} and year to date</caption>
            <thead>
              <tr>
                <th scope="col" style={{ textAlign: "left" }}>
                  Account
                </th>
                <th scope="col">Actual</th>
                <th scope="col">Budget</th>
                <th scope="col">Difference</th>
                <th scope="col">YTD actual</th>
                <th scope="col">YTD budget</th>
                <th scope="col">YTD difference</th>
                <th scope="col">Prior month</th>
              </tr>
            </thead>
            <tbody>
              {(["Revenue", "Expense"] as const).map((cls) =>
                report.rows!
                  .filter((row) => row.class === cls)
                  .map((row) => (
                    <tr key={row.account_id}>
                      <th scope="row" style={{ textAlign: "left", fontWeight: 400 }}>
                        {row.name}
                        {row.unbudgeted && <small> · not in the budget</small>}
                      </th>
                      <td>{show(row.month.actual)}</td>
                      <td>{show(row.month.budget)}</td>
                      {variance(row.month)}
                      <td>{show(row.ytd.actual)}</td>
                      <td>{show(row.ytd.budget)}</td>
                      {variance(row.ytd)}
                      <td>{show(row.prior_month)}</td>
                    </tr>
                  )),
              )}
              {totalRow("Income", report.totals.income_month, report.totals.income_ytd)}
              {totalRow("Expenses", report.totals.expense_month, report.totals.expense_ytd)}
              {totalRow("Net income", report.totals.net_month, report.totals.net_ytd)}
            </tbody>
          </table>
        </>
      )}
      {!readOnly && <details open={report.state === "no_budget"}>
        <summary>Budget versions</summary>
        <p className="note">
          Every import or remapping is a new version; approving one makes it the baseline, and an approved version never changes, so the original plan stays
          reproducible. The agent can suggest a draft, but only a person approves a budget.
        </p>
        <ul>
          {report.versions.length === 0 && <li className="empty">No budget imported yet.</li>}
          {report.versions.map((v) => (
            <li key={v.id}>
              <strong>{v.name}</strong> · #{v.id} · {v.lines.length} lines · {v.source} · {v.currency} ·{" "}
              {v.approved_at ? <span className="status ok">Approved by {v.approved_by}</span> : <span className="status warn">Draft</span>}
              {v.problems.length > 0 && <small> · {v.problems.join(" ")}</small>}{" "}
              {!v.approved_at && v.problems.length === 0 && (
                <button type="button" disabled={busy} onClick={() => void act({ action: "approve", id: v.id }, `Approved ${v.name} as the baseline.`)}>
                  Approve as baseline
                </button>
              )}
              <details>
                <summary aria-label={`Lines and monthly totals of ${v.name} #${v.id}`}>Lines and monthly totals</summary>
                <table>
                  <caption>{v.name}, as imported</caption>
                  <thead>
                    <tr>
                      <th scope="col" style={{ textAlign: "left" }}>
                        Budget line
                      </th>
                      <th scope="col" style={{ textAlign: "left" }}>
                        Ledger account
                      </th>
                      {Object.keys(v.totals)
                        .sort()
                        .map((month) => (
                          <th scope="col" key={month}>
                            {month}
                          </th>
                        ))}
                    </tr>
                  </thead>
                  <tbody>
                    {v.lines.map((l, i) => (
                      <tr key={i}>
                        <th scope="row" style={{ textAlign: "left", fontWeight: 400 }}>
                          {l.account}
                        </th>
                        <td style={{ textAlign: "left" }}>{names[l.account_id] ?? (l.account_id ? `#${l.account_id} (not in the chart)` : "Not mapped")}</td>
                        {Object.keys(v.totals)
                          .sort()
                          .map((month) => (
                            <td key={month}>{amount(l.months[month] ?? "0.00", v.currency)}</td>
                          ))}
                      </tr>
                    ))}
                    {(["income", "expense", "net", "unmapped"] as const).map((total) => (
                      <tr key={total}>
                        <th scope="row" colSpan={2} style={{ textAlign: "left" }}>
                          {{ income: "Income", expense: "Expenses", net: "Net income", unmapped: "Not mapped" }[total]}
                        </th>
                        {Object.keys(v.totals)
                          .sort()
                          .map((month) => (
                            <td key={month}>{amount(v.totals[month][total], v.currency)}</td>
                          ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
                {report.accounts.length > 0 && (
                  <form
                    onSubmit={(e) => {
                      e.preventDefault();
                      const mapping = Object.fromEntries([...new FormData(e.currentTarget)].filter(([key, value]) => value !== "" && value !== v.lines[Number(key)].account_id));
                      void act({ action: "remap", id: v.id, mapping }, "Saved the mapping as a new draft.");
                    }}
                  >
                    <fieldset disabled={busy}>
                      <legend>Map lines to ledger accounts (saves a new draft; this version is kept)</legend>
                      {v.lines.map((l, i) => (
                        <label key={i}>
                          {l.account}
                          <select name={String(i)} defaultValue={l.account_id}>
                            <option value="">Choose the ledger account</option>
                            {report.accounts.map((a) => (
                              <option key={a.id} value={a.id}>
                                {a.name} ({a.class === "Revenue" ? "income" : "expense"})
                              </option>
                            ))}
                          </select>
                        </label>
                      ))}
                      <button>Save mapping as a new draft</button>
                    </fieldset>
                  </form>
                )}
              </details>
            </li>
          ))}
        </ul>
        <form onSubmit={(e) => void importBudget(e)}>
          <fieldset disabled={busy}>
            <legend>Import a budget</legend>
            <label>
              Name
              <input name="name" required maxLength={200} placeholder="FY2026 budget" />
            </label>
            <label>
              CSV file: a first row of Account and months as YYYY-MM, then one row per account, amounts as the Profit and Loss shows them
              <input name="file" type="file" accept=".csv,text/csv" />
            </label>
            <label>
              Or paste the CSV
              <textarea name="csv" maxLength={524288} placeholder={"Account,2026-01,2026-02\nSales,10000,10500\nRent,1500,1500"} />
            </label>
            <button>Import as a draft</button>
          </fieldset>
        </form>
      </details>}
    </section>
  );
}
