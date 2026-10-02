import { Fragment, useEffect, useState } from "react";
import { AskAgent } from "../dashboard/AskAgent";
import { LedgerSelection } from "./Coverage";
import { money, workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Line = { account_id?: string; name: string; amount: string; prior: string | null; change: string | null };
type Section = { name: string; lines: Line[]; total: Line };
type Pack =
  | { state: "waiting"; reason: string }
  | {
      state: "ready";
      entity: string;
      period: string;
      basis: string;
      currency: string;
      source: string;
      snapshot_id: string;
      as_of: string;
      stale: boolean;
      status: Status;
      complete: boolean;
      note: string;
      prior?: { close_id: string; period: string; snapshot_id: string; as_of: string; stale: boolean; status: Status };
      reclassified: string[];
      comparison_unavailable?: string;
      profit_and_loss: { sections: Section[]; measures: Line[] };
      balance_sheet: { sections: Section[]; earnings: Line; total_assets: Line; total_liabilities: Line; total_equity: Line; difference: string };
      working_capital: Record<"book_cash" | "receivables" | "payables" | "current_assets" | "current_liabilities" | "working_capital", Line> | { unavailable: string };
      cash_flow: { unavailable: string };
    };

type Status = "provisional" | "signed_off" | "unknown";

const statusLabels = { provisional: ["status warn", "Provisional"], signed_off: ["status ok", "Sign-off stands"], unknown: ["status danger", "Status unknown"] } as const;

// The month's management accounts from the close's captured reports. Every figure links to its ledger rows when the
// capture holds them; nothing here is published or sent.
export function Reports({ closeId }: { closeId: string }) {
  const [pack, setPack] = useState<Pack>();
  const [error, setError] = useState("");
  const [account, setAccount] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/management-pack`)
      .then((data) => live && setPack(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "The management pack is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId]);
  if (!pack) return error ? <p role="alert">{error}</p> : <p role="status">Preparing the management pack…</p>;
  if (pack.state === "waiting") return <p className="empty">{pack.reason}</p>;
  // Thousands separators and a true minus on the exact decimal string; no float conversion.
  const show = (amount: string | null) => {
    if (amount === null) return "–";
    const [whole, cents] = amount.replace("-", "").split(".");
    return (amount.startsWith("-") ? "−" : "") + money(`${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",")}.${cents}`, pack.currency);
  };
  const compared = !!pack.prior;
  // Xero captures have no ledger-row reader yet, so their figures are not drillable.
  const drill = pack.source === "quickbooks";
  const row = (line: Line, key: string, strong = false) => (
    <tr key={key}>
      <th scope="row" style={{ textAlign: "left", fontWeight: strong ? 600 : 400 }}>
        {drill && line.account_id ? (
          <button type="button" className="cell-link" aria-expanded={account === line.account_id} onClick={() => {
              setAccount(account === line.account_id ? "" : line.account_id!);
              setTimeout(() => document.getElementById("figure-ledger")?.scrollIntoView({ block: "start" }));
            }}>
            {line.name}
          </button>
        ) : (
          line.name
        )}
      </th>
      <td style={{ whiteSpace: "nowrap" }}>{show(line.amount)}</td>
      {compared && <td style={{ whiteSpace: "nowrap" }}>{show(line.prior)}</td>}
      {compared && <td style={{ whiteSpace: "nowrap" }}>{show(line.change)}</td>}
    </tr>
  );
  const table = (caption: string, sections: Section[], after: Line[]) => (
    <table>
      <caption>{caption}</caption>
      <thead>
        <tr>
          <th style={{ textAlign: "left" }}>Account</th>
          <th>{pack.period}</th>
          {compared && <th>{pack.prior!.period}</th>}
          {compared && <th>Change</th>}
        </tr>
      </thead>
      <tbody>
        {sections.map((section) => (
          <Fragment key={section.name}>
            <tr>
              <th colSpan={compared ? 4 : 2} style={{ textAlign: "left" }}>
                {section.name}
              </th>
            </tr>
            {section.lines.map((line) => row(line, `${section.name}-${line.account_id}`))}
            {row(section.total, `${section.name}-total`, true)}
          </Fragment>
        ))}
        {after.map((line) => row(line, `summary-${line.name}`, true))}
      </tbody>
    </table>
  );
  const [statusClass, statusText] = statusLabels[pack.status];
  const bs = pack.balance_sheet;
  const wc = pack.working_capital;
  const prior = pack.prior;
  return (
    <section aria-label="Management pack">
      <AskAgent
        closeId={closeId}
        description="Ask about these results. The agent reads the same captured reports and cites them."
        placeholder="Why did operating profit change this month?"
        suggestions={["Explain the largest movements in profit this month", "What drove the change in working capital?", "Which accounts make up book cash?"]}
      />
      <p>
        <strong>
          {pack.entity} · {pack.period}
        </strong>{" "}
        · {pack.basis} basis · {pack.currency} · captured from {pack.source === "quickbooks" ? "QuickBooks" : "Xero"} {new Date(pack.as_of).toLocaleString()} ·{" "}
        <span className={statusClass}>{statusText}</span>
      </p>
      {pack.stale && <p role="alert">This capture is no longer current: its connection or evidence changed. Capture the month again before relying on these figures.</p>}
      {!pack.complete && <p role="alert">Not complete: an account is unclassified or a total does not reconcile. The difference is shown below; nothing is absorbed.</p>}
      <p className="note">
        {pack.note}{" "}
        {prior ? (
          <>
            Compared with {prior.period} (<span className={statusLabels[prior.status][0]}>{statusLabels[prior.status][1]}</span>), captured {new Date(prior.as_of).toLocaleString()}
            {prior.stale ? "; that capture is no longer current" : ""}. Totals are the sums of the accounts listed above them.
          </>
        ) : (
          `No comparison: ${pack.comparison_unavailable}`
        )}
      </p>
      {pack.reclassified.length > 0 && <p className="note">Shown under this month's classification, which differs from last month's: {pack.reclassified.join(", ")}.</p>}
      <SectionHeader title="Working capital" />
      {"unavailable" in wc ? (
        <p className="note">{wc.unavailable}</p>
      ) : (
        table(`At the end of ${pack.period}`, [], [wc.book_cash, wc.receivables, wc.payables, wc.current_assets, wc.current_liabilities, wc.working_capital])
      )}
      <SectionHeader title="Profit and loss" />
      {table(`Activity in ${pack.period}`, pack.profit_and_loss.sections, pack.profit_and_loss.measures)}
      <SectionHeader title="Balance sheet" />
      {table(`Balances at the end of ${pack.period}`, bs.sections, [bs.earnings, bs.total_assets, bs.total_liabilities, bs.total_equity])}
      {bs.difference !== "0.00" && <p role="alert">Assets differ from liabilities and equity by {show(bs.difference)}.</p>}
      <SectionHeader title="Cash flow" />
      <p className="note">{pack.cash_flow.unavailable}</p>
      {drill && account && (
        <>
          <SectionHeader id="figure-ledger" title={`Ledger rows behind this figure, ${pack.period}`} />
          <LedgerSelection key={`this-${account}`} closeId={closeId} snapshotId={pack.snapshot_id} accountId={account} stale={pack.stale} suggestions={[]} />
          {prior && (
            <>
              <SectionHeader title={`Ledger rows behind this figure, ${prior.period}`} />
              <LedgerSelection key={`prior-${account}`} closeId={prior.close_id} snapshotId={prior.snapshot_id} accountId={account} stale={prior.stale} suggestions={[]} />
            </>
          )}
        </>
      )}
    </section>
  );
}
