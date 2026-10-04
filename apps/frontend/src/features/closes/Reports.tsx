import { Fragment, useEffect, useState } from "react";
import { AskAgent } from "../dashboard/AskAgent";
import { LedgerSelection } from "./Coverage";
import { amount, workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Line = { account_id?: string; name: string; amount: string; prior: string | null; change: string | null };
type Section = { name: string; lines: Line[]; total: Line };
export type Pack =
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
      overall: Status;
      digest: string;
      complete: boolean;
      draft_commentary?: string;
      note: string;
      prior?: { close_id: string; period: string; snapshot_id: string; as_of: string; stale: boolean; status: Status };
      reclassified: string[];
      comparison_unavailable?: string;
      profit_and_loss: { sections: Section[]; measures: Line[] };
      balance_sheet: { sections: Section[]; earnings: Line; total_assets: Line; total_liabilities: Line; total_equity: Line; difference: string };
      working_capital: Record<"book_cash" | "receivables" | "payables" | "current_assets" | "current_liabilities" | "working_capital", Line> | { unavailable: string };
      cash_flow: { unavailable: string } | { sections: Section[]; net_change: Line; beginning: Line; ending: Line; book_cash: string; reconciled: boolean; differences: string[] };
    };

type Status = "provisional" | "signed_off" | "unknown";

const statusLabels = { provisional: ["status warn", "Provisional"], signed_off: ["status ok", "Sign-off stands"], unknown: ["status danger", "Status unknown"] } as const;

// The month's management accounts from the close's captured reports. Every figure links to its ledger rows when the
// capture holds them; a reviewer publishes a signed-off pack as a frozen version.
export function Reports({ closeId }: { closeId: string }) {
  const [pack, setPack] = useState<Pack>();
  const [error, setError] = useState("");
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
  if (pack.state === "waiting")
    return (
      <>
        <p className="empty">{pack.reason}</p>
        <Publication closeId={closeId} pack={pack} />
      </>
    );
  return <PackView closeId={closeId} pack={pack} staff />;
}

// A pack's statements, live or as published. Staff can ask the agent, open each figure's ledger rows and publish;
// an executive reads the figures alone.
export function PackView({ closeId, pack, staff = false }: { closeId: string; pack: Extract<Pack, { state: "ready" }>; staff?: boolean }) {
  const [account, setAccount] = useState("");
  const show = (value: string | null) => (value === null ? "–" : amount(value, pack.currency));
  const compared = !!pack.prior;
  // Xero captures have no ledger-row reader yet, so their figures are not drillable.
  const drill = staff && pack.source === "quickbooks";
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
  const flow = pack.cash_flow;
  const prior = pack.prior;
  return (
    <section aria-label="Management pack">
      {staff && (
        <AskAgent
          closeId={closeId}
          description="Ask about these results. The agent reads the same captured reports and cites them."
          placeholder="Why did operating profit change this month?"
          suggestions={["Explain the largest movements in profit this month", "What drove the change in working capital?", "Which accounts make up book cash?"]}
        />
      )}
      <p>
        <strong>
          {pack.entity} · {pack.period}
        </strong>{" "}
        · {pack.basis} basis · {pack.currency} · captured from {pack.source === "quickbooks" ? "QuickBooks" : "Xero"} {new Date(pack.as_of).toLocaleString()} ·{" "}
        <span className={statusClass}>{statusText}</span>
      </p>
      {pack.stale && <p role="alert">This capture is no longer current: its connection or evidence changed. Capture the month again before relying on these figures.</p>}
      {!pack.complete && <p role="alert">Not complete: an account is unclassified, a total does not reconcile, or there is no reconciled cash-flow statement. What is missing is shown below; nothing is absorbed.</p>}
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
      {"unavailable" in flow ? (
        <p className="note">{flow.unavailable}</p>
      ) : (
        <>
          <table>
            <caption>The ledger's Statement of Cash Flows for {pack.period}</caption>
            <thead>
              <tr>
                <th style={{ textAlign: "left" }}>Line</th>
                <th>{pack.period}</th>
              </tr>
            </thead>
            <tbody>
              {flow.sections.map((section) => (
                <Fragment key={section.name}>
                  <tr>
                    <th colSpan={2} style={{ textAlign: "left" }}>
                      {section.name}
                    </th>
                  </tr>
                  {section.lines.map((line, i) => (
                    <tr key={i}>
                      <th scope="row" style={{ textAlign: "left", fontWeight: "normal" }}>
                        {line.name}
                      </th>
                      <td>{show(line.amount)}</td>
                    </tr>
                  ))}
                  <tr>
                    <th scope="row" style={{ textAlign: "left" }}>
                      {section.total.name}
                    </th>
                    <td>{show(section.total.amount)}</td>
                  </tr>
                </Fragment>
              ))}
              {[flow.net_change, flow.beginning, flow.ending].map((line) => (
                <tr key={line.name}>
                  <th scope="row" style={{ textAlign: "left" }}>
                    {line.name}
                  </th>
                  <td>{show(line.amount)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {flow.reconciled ? (
            <p role="status">Ties to the books: the statement's ending cash is the month-end book cash of {show(flow.book_cash)}.</p>
          ) : (
            <p role="alert">Does not tie to the books. {flow.differences.join(" ")}</p>
          )}
        </>
      )}
      {staff && <Publication closeId={closeId} pack={pack} />}
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

type Published = {
  id: string;
  version: number;
  commentary: string;
  reason: string;
  supersedes: string | null;
  published_by: string;
  published_at: string;
  state: keyof typeof publishedStates;
  acknowledgments: { subject: string; at: string }[];
};
const publishedStates = {
  current: ["status ok", "Current"],
  superseded: ["status info", "Superseded"],
  needs_correction: ["status danger", "Books changed: needs a correction"],
  signoff_lapsed: ["status warn", "Sign-off no longer stands: review before relying on it"],
  unverified: ["status warn", "Live figures unavailable: cannot confirm it is current"],
} as const;

// Publishing freezes this pack with the reviewer's commentary; exports come from the frozen version, never the live figures.
export function Publication({ closeId, pack, onOpen }: { closeId: string; pack?: Pack; onOpen?: (id: string) => void }) {
  const [list, setList] = useState<Published[]>();
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/management-pack/publications`)
      .then((data) => live && setList(data.publications))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Publications are unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, version]);
  async function act(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/management-pack/publications`, body);
      setSaved(done);
      setVersion((n) => n + 1);
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : "The pack could not be published.");
      return false;
    } finally {
      setBusy(false);
    }
  }
  async function download(p: Published, format: "csv" | "pdf") {
    try {
      const blob = await workspaceRequest(`/${closeId}/management-pack/publications/${p.id}/export?format=${format}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `management pack v${p.version}.${format}`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The export is unavailable.");
    }
  }
  const latest = list?.[0];
  const publishable = pack?.state === "ready" && pack.overall === "signed_off" && !pack.stale && pack.complete;
  return (
    <>
      <SectionHeader title="Publication" />
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">{saved}</p>}
      {!list ? null : list.length === 0 ? <p className="empty">Not published yet.</p> : (
        <ul>
          {list.map((p) => (
            <li key={p.id}>
              <strong>Version {p.version}</strong> · published by {p.published_by} {new Date(p.published_at).toLocaleString()} ·{" "}
              <span className={publishedStates[p.state][0]}>{publishedStates[p.state][1]}</span>
              <p>{p.commentary}</p>
              {p.reason && <p className="note">Corrects version {list.find((q) => q.id === p.supersedes)?.version}: {p.reason}</p>}
              <p className="note">
                {p.acknowledgments.length === 0 ? "Not acknowledged yet." : `Acknowledged by ${p.acknowledgments.map((a) => `${a.subject} (${new Date(a.at).toLocaleDateString()})`).join(", ")}.`}
              </p>
              {onOpen && <button type="button" onClick={() => onOpen(p.id)}>Read version {p.version}</button>}{" "}
              <button type="button" onClick={() => void download(p, "pdf")}>PDF</button> <button type="button" onClick={() => void download(p, "csv")}>CSV</button>{" "}
              <button type="button" disabled={busy} onClick={() => void act({ action: "acknowledge", publication_id: p.id }, `Acknowledged version ${p.version}.`)}>Acknowledge</button>
            </li>
          ))}
        </ul>
      )}
      {!pack ? null : !publishable ? (
        <p className="note">Publish once the capture is current, the pack is complete and the sign-off of every month shown stands.</p>
      ) : latest && latest.state === "current" ? null : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const element = e.currentTarget;
            const form = new FormData(element);
            void act({ action: "publish", digest: pack?.state === "ready" && pack.digest, commentary: form.get("commentary"), reason: form.get("reason") ?? "", confirmed: form.has("confirmed") }, latest ? "Published a correction." : "Published.").then((ok) => ok && element.reset());
          }}
        >
          <fieldset disabled={busy}>
            <legend>{latest ? "Publish a correction" : "Publish this pack"}</legend>
            <label>
              Commentary: what moved and why, as you have verified it (drafted from the figures; add why each moved)
              <textarea name="commentary" required maxLength={4000} defaultValue={pack?.state === "ready" ? pack.draft_commentary : ""} />
            </label>
            {latest && (
              <label>
                Why this corrects version {latest.version}
                <textarea name="reason" required maxLength={2000} />
              </label>
            )}
            <label><input type="checkbox" name="confirmed" required /> I reviewed these figures; the published version and its exports will not change.</label>
            <button className="primary">Publish</button>
          </fieldset>
        </form>
      )}
    </>
  );
}
