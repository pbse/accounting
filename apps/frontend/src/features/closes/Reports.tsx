import { workspaceKey, workspaceQuery } from "../../data/queries";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { Fragment, useEffect, useState } from "react";
import { LedgerSelection } from "./Coverage";
import { amount, workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

export type Line = { account_id?: string; name: string; amount: string; prior: string | null; change: string | null };
export type Section = { name: string; lines: Line[]; total: Line };
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
      open: boolean;
      draft_commentary?: string;
      note: string;
      // source is the prior month's own, when it differs from this month's (a company confirmed across sources).
      prior?: { close_id: string; period: string; snapshot_id: string; as_of: string; stale: boolean; status: Status; source?: string; file?: string };
      reclassified: string[];
      comparison_unavailable?: string;
      profit_and_loss: { sections: Section[]; measures: Line[] };
      profit_and_loss_unavailable?: string;
      file?: string;
      balance_sheet: { sections: Section[]; earnings: Line; total_assets: Line; total_liabilities: Line; total_equity: Line; difference: string };
      working_capital: Record<"book_cash" | "receivables" | "payables" | "current_assets" | "current_liabilities" | "working_capital", Line> | { unavailable: string };
      cash_flow: { unavailable: string } | { sections: Section[]; net_change: Line; beginning: Line; ending: Line; book_cash: string; reconciled: boolean; differences: string[]; disclosure?: string };
    };

// A reviewed comparative is a prior month accepted from its file that is no close: shown as such, never signed off.
type Status = "provisional" | "signed_off" | "unknown" | "comparative";

const statusLabels = {
  provisional: ["status warn", "Provisional"],
  signed_off: ["status ok", "Sign-off stands"],
  unknown: ["status danger", "Status unknown"],
  comparative: ["status", "Reviewed comparative"],
} as const;

// The month's management accounts from the close's captured reports. Every figure links to its ledger rows when the
// capture holds them; a reviewer publishes a signed-off pack as a frozen version. account opens one figure's rows.
export function Reports({ closeId, account = "" }: { closeId: string; account?: string }) {
  const queryClient = useQueryClient();
  const read = useQuery(workspaceQuery<Pack>(`/${closeId}/management-pack`));
  const pack = read.data;
  const error = read.error ? read.error.message || "The management pack is unavailable." : "";
  // An import answers with the pack, which replaces the read one.
  const setPack = (next: Pack) => queryClient.setQueryData(workspaceKey(`/${closeId}/management-pack`), next);
  if (!pack) return error ? <p role="alert">{error}</p> : <p role="status">Preparing the management pack…</p>;
  if (pack.state === "waiting")
    return (
      <>
        <p className="empty">{pack.reason}</p>
        <Publication closeId={closeId} pack={pack} />
      </>
    );
  return <PackView closeId={closeId} pack={pack} staff onPack={setPack} account={account} />;
}

// A pack's statements, live or as published. Staff can ask the agent, open each figure's ledger rows and publish;
// an executive reads the figures alone.
export function PackView({ closeId, pack, staff = false, onPack, account: opened = "" }: { closeId: string; pack: Extract<Pack, { state: "ready" }>; staff?: boolean; onPack?: (pack: Pack) => void; account?: string }) {
  const dates = useDates();
  const [account, setAccount] = useState(opened);
  const show = (value: string | null) => (value === null ? "–" : amount(value, pack.currency));
  const compared = !!pack.prior;
  // Xero captures have no ledger-row reader yet, so their figures are not drillable.
  // A figure opens its ledger rows: a QuickBooks capture's, or the lines of a month's ledger file.
  const drill = staff && (pack.source === "quickbooks" || pack.source === "files");
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
      <p>
        <strong>
          {pack.entity} · {pack.period}
        </strong>{" "}
        · {pack.basis} basis · {pack.currency} ·{" "}
        {pack.source === "files"
          ? `from the accepted file ${pack.file ?? ""}, as of ${dates.calendarDate(pack.as_of)}`
          : `captured from ${pack.source === "quickbooks" ? "QuickBooks" : "Xero"} ${dates.dateTime(pack.as_of)}`}{" "}
        ·{" "}
        <span className={statusClass}>{statusText}</span>
      </p>
      {pack.stale && <p role="alert">This capture is no longer current: its connection or evidence changed. Capture the month again before relying on these figures.</p>}
      {!pack.complete && <p role="alert">Not complete: an account is unclassified, a total does not reconcile, or there is no reconciled cash-flow statement. What is missing is shown below; nothing is absorbed.</p>}
      <p className="note">
        {pack.note}{" "}
        {prior ? (
          <>
            Compared with {prior.period} (<span className={statusLabels[prior.status][0]}>{statusLabels[prior.status][1]}</span>), {(prior.source ?? pack.source) === "files" ? `as of ${dates.calendarDate(prior.as_of)}` : `captured ${dates.dateTime(prior.as_of)}`}
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
      {pack.profit_and_loss_unavailable ? (
        <p className="note">The month's activity is unavailable: {pack.profit_and_loss_unavailable}. It is never estimated.</p>
      ) : (
        table(`Activity in ${pack.period}`, pack.profit_and_loss.sections, pack.profit_and_loss.measures)
      )}
      <SectionHeader title="Balance sheet" />
      {table(`Balances at the end of ${pack.period}`, bs.sections, [bs.earnings, bs.total_assets, bs.total_liabilities, bs.total_equity])}
      {bs.difference !== "0.00" && <p role="alert">Assets differ from liabilities and equity by {show(bs.difference)}.</p>}
      <SectionHeader title="Cash flow" />
      {"unavailable" in flow ? (
        <p className="note">{flow.unavailable}</p>
      ) : (
        <>
          <table>
            <caption>{flow.disclosure ? `The firm's Statement of Cash Flows for ${pack.period}, as imported` : `The ledger's Statement of Cash Flows for ${pack.period}`}</caption>
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
      {onPack && (pack.source === "xero" || pack.source === "files") && (pack.open ? <ImportCashFlow closeId={closeId} pack={pack} onPack={onPack} /> : <p className="note">The close's sign-off covers its cash-flow statement: reopen the sign-off to import or replace it.</p>)}
      {staff && <Publication closeId={closeId} pack={pack} />}
      {drill && account && (
        <>
          <SectionHeader id="figure-ledger" title={`Ledger rows behind this figure, ${pack.period}`} />
          {pack.source === "files" ? (
            <FileLines key={`this-${pack.snapshot_id}-${account}`} closeId={closeId} accountId={account} />
          ) : (
            <LedgerSelection key={`this-${account}`} closeId={closeId} snapshotId={pack.snapshot_id} accountId={account} stale={pack.stale} suggestions={[]} />
          )}
          {/* Last month's rows are its own ledger's: drilled into only when they are QuickBooks', as this month's are. */}
          {prior && (prior.source ?? pack.source) === "quickbooks" && (
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

// Xero and a client's files have no Statement of Cash Flows to capture, so the firm imports its own; the sign-off covers it.
function ImportCashFlow({ closeId, pack, onPack }: { closeId: string; pack: Extract<Pack, { state: "ready" }>; onPack: (pack: Pack) => void }) {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const form = new FormData(e.currentTarget);
        const file = form.get("file") as File;
        setBusy(true);
        setError("");
        file
          .text()
          .then((csv) => workspaceRequest(`/${closeId}/cash-flow`, { csv, source: file.name, confirmed: form.has("confirmed") }))
          .then(onPack)
          .catch((e) => setError(e instanceof Error ? e.message : "The statement could not be imported."))
          .finally(() => setBusy(false));
      }}
    >
      <fieldset disabled={busy}>
        <legend>Import or replace the firm's statement (needs a role that can both review and sign off)</legend>
        {error && <p role="alert">{error}</p>}
        <label>
          CSV with the columns Section, Line, Amount: a row per line under Operating activities, Investing activities, Financing activities or Effect of exchange rate changes, outflows negative and no totals, plus a Beginning cash and an Ending cash row with an empty Line
          <input name="file" type="file" accept=".csv,text/csv" required />
        </label>
        <label>
          <input type="checkbox" name="confirmed" required /> I reviewed this statement's classification and cash scope; it is for {pack.entity}, {pack.period}, in {pack.currency}, with amounts in whole {pack.currency} units, not thousands.
        </label>
        <button>Import</button>
      </fieldset>
    </form>
  );
}

// An explanation of one line that moved, drafted from the ledger and then reviewed; published with the pack.
type Explanation = { account_id: string; name: string; change: string; material: boolean; text: string };
export type Published = {
  id: string;
  version: number;
  commentary: string;
  explanations?: Explanation[];
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

// A published version's PDF or CSV, saved as a file; it fails with the reason the export is unavailable.
export async function downloadPublication(closeId: string, p: Published, format: "csv" | "pdf") {
  const blob = await workspaceRequest(`/${closeId}/management-pack/publications/${p.id}/export?format=${format}`, undefined, true);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `management pack v${p.version}.${format}`;
  a.click();
  URL.revokeObjectURL(url);
}

// Publishing freezes this pack with the reviewer's commentary; exports come from the frozen version, never the live figures.
export function Publication({ closeId, pack, onOpen }: { closeId: string; pack?: Pack; onOpen?: (id: string) => void }) {
  const dates = useDates();
  const read = useQuery(workspaceQuery<{ publications: Published[] }>(`/${closeId}/management-pack/publications`));
  const list = read.data?.publications;
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "Publications are unavailable." : "");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  async function act(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/management-pack/publications`, body);
      setSaved(done);
      void read.refetch();
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
      await downloadPublication(closeId, p, format);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The export is unavailable.");
    }
  }
  const latest = list?.[0];
  const publishable = pack?.state === "ready" && pack.overall === "signed_off" && !pack.stale && pack.complete;
  // The agent's drafted explanation of each line that moved (material movements first), the firm's until published.
  const drafts = useQuery({ ...workspaceQuery<{ digest: string; explanations: Explanation[] }>(`/${closeId}/management-pack/explanations`), enabled: publishable });
  // The drafts are for these figures only: drafted for another digest, they are read again before anything is published.
  const draftsCurrent = pack?.state === "ready" && drafts.data?.digest === pack.digest;
  const packDigest = pack?.state === "ready" ? pack.digest : "";
  const queries = useQueryClient();
  useEffect(() => {
    if (publishable && drafts.data && drafts.data.digest !== packDigest) {
      // Either side may be the newer: read both again.
      void drafts.refetch();
      void queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/management-pack`) });
    }
  }, [publishable, packDigest, drafts.data?.digest]);
  return (
    <>
      <SectionHeader title="Publication" />
      {error && (
        <p role="alert">
          {error}{" "}
          {read.error && (
            <button type="button" onClick={() => void read.refetch()}>
              Try again
            </button>
          )}
        </p>
      )}
      {saved && <p role="status">{saved}</p>}
      {!list ? null : list.length === 0 ? <p className="empty">Not published yet.</p> : (
        <ul>
          {list.map((p) => (
            <li key={p.id}>
              <strong>Version {p.version}</strong> · published by {p.published_by} {dates.dateTime(p.published_at)} ·{" "}
              <span className={publishedStates[p.state][0]}>{publishedStates[p.state][1]}</span>
              <p>{p.commentary}</p>
              {(p.explanations?.length ?? 0) > 0 && <ul aria-label="Line explanations">{p.explanations!.map((e) => <li key={e.account_id}>{e.text}</li>)}</ul>}
              {p.reason && <p className="note">Corrects version {list.find((q) => q.id === p.supersedes)?.version}: {p.reason}</p>}
              <p className="note">
                {p.acknowledgments.length === 0 ? "Not acknowledged yet." : `Acknowledged by ${p.acknowledgments.map((a) => `${a.subject} (${dates.date(a.at)})`).join(", ")}.`}
              </p>
              {onOpen && <button type="button" onClick={() => onOpen(p.id)}>Read version {p.version}</button>}{" "}
              <button type="button" onClick={() => void download(p, "pdf")}>PDF</button> <button type="button" onClick={() => void download(p, "csv")}>CSV</button>{" "}
              <button type="button" disabled={busy} onClick={() => void act({ action: "acknowledge", publication_id: p.id }, `Acknowledged version ${p.version}.`)}>Acknowledge</button>
            </li>
          ))}
        </ul>
      )}
      {/* Publishing waits until the versions are read: an unread list is never taken for none. */}
      {!pack || !list ? null : !publishable ? (
        <p className="note">Publish once the capture is current, the pack is complete and the sign-off of every month shown stands.</p>
      ) : (
        <details open={!latest || latest.state !== "current"}>
        <summary>{latest?.state === "current" ? "Revise the commentary or explanations" : latest ? "Publish a correction" : "Publish this pack"}</summary>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            const element = e.currentTarget;
            const form = new FormData(element);
            const explanations = (drafts.data?.explanations ?? []).map((d) => ({ account_id: d.account_id, text: String(form.get(`explanation:${d.account_id}`) ?? "").trim() })).filter((d) => d.text !== "");
            void act({ action: "publish", digest: pack?.state === "ready" && pack.digest, commentary: form.get("commentary"), explanations, reason: form.get("reason") ?? "", confirmed: form.has("confirmed") }, latest ? "Published a new version." : "Published.").then((ok) => ok && element.reset());
          }}
        >
          <fieldset disabled={busy}>
            <legend>{latest ? "Publish a new version" : "Publish this pack"}</legend>
            <label>
              Commentary: what moved and why, as you have verified it (drafted from the figures; add why each moved)
              <textarea name="commentary" required maxLength={4000} defaultValue={pack?.state === "ready" ? pack.draft_commentary : ""} />
            </label>
            {drafts.error && <p role="alert">The drafted line explanations could not be read. <button type="button" onClick={() => void drafts.refetch()}>Try again</button></p>}
            {!draftsCurrent && !drafts.error && <p role="status">Drafting the line explanations…</p>}
            {draftsCurrent && drafts.data!.explanations.length > 0 && (
              <fieldset>
                <legend>Line explanations, published with the pack (drafted from the ledger: confirm each, edit, or say plainly the cause is not known)</legend>
                {drafts.data!.explanations.map((d) => (
                  <label key={d.account_id}>
                    {d.name}{d.material ? " · material" : ""}
                    <textarea name={`explanation:${d.account_id}`} maxLength={600} required={d.material} defaultValue={d.text} />
                  </label>
                ))}
              </fieldset>
            )}
            {latest && (
              <label>
                Why this version replaces version {latest.version}
                <textarea name="reason" required maxLength={2000} />
              </label>
            )}
            <label><input type="checkbox" name="confirmed" required /> I reviewed these figures; the published version and its exports will not change.</label>
            <button className="primary" disabled={!draftsCurrent}>Publish</button>
          </fieldset>
        </form>
        </details>
      )}
    </>
  );
}

// A month on files' ledger lines for one account: the transactions behind a figure, twenty at a time, searchable. A month
// whose file is a Trial Balance has none.
type FileLine = { location: string; date?: string; reference?: string; party?: string; memo?: string; amount?: string };
function FileLines({ closeId, accountId }: { closeId: string; accountId: string }) {
  const [query, setQuery] = useState("");
  const [page, setPage] = useState<{ offset: number; total: number; rows: FileLine[]; query: string; file: string } | null>(null);
  const [error, setError] = useState("");
  async function read(offset: number, search = query) {
    setError("");
    try {
      setPage(await workspaceRequest(`/${closeId}/files/ledger?account_id=${encodeURIComponent(accountId)}&offset=${offset}&query=${encodeURIComponent(search)}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "The ledger lines could not be read.");
    }
  }
  useEffect(() => {
    void read(0, "");
  }, [closeId, accountId]);
  return (
    <div>
      <label>
        Find a line by its date, number, payee or description
        <input type="search" value={query} maxLength={100} onChange={(e) => setQuery(e.target.value)} />
      </label>
      <button type="button" onClick={() => void read(0)}>
        Find
      </button>
      {error && <p role="alert">{error}</p>}
      {page &&
        (page.total === 0 ? (
          <p>No lines{page.query ? " match" : ""} in this month's file for this account{page.query ? "" : ": its file is a Trial Balance, or the account had no transactions"}.</p>
        ) : (
          <>
            <p>
              {page.total} line{page.total === 1 ? "" : "s"} in {page.file}. Showing {page.offset + 1}–{page.offset + page.rows.length}.
            </p>
            <ul>
              {page.rows.map((l) => (
                <li key={l.location}>
                  {[l.date, l.reference && `#${l.reference}`, l.party, l.memo].filter(Boolean).join(" · ")}: {l.amount} ({l.location})
                </li>
              ))}
            </ul>
            {page.offset + page.rows.length < page.total && (
              <button type="button" onClick={() => void read(page.offset + 20, page.query)}>
                Next lines
              </button>
            )}
            {page.offset > 0 && (
              <button type="button" onClick={() => void read(Math.max(0, page.offset - 20), page.query)}>
                Previous lines
              </button>
            )}
          </>
        ))}
    </div>
  );
}
