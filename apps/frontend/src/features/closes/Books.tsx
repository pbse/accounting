import { useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { clientPage } from "../../data/clients";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { Aging } from "./Aging";
import { BillCorrection } from "./BillCorrection";
import { CreditApplications } from "./CreditApplications";
import { Exceptions } from "./Exceptions";
import { Journals } from "./Journals";
import { Flux } from "./Flux";
import type { Status } from "./Goal";
import { Matching } from "./Matching";
import { MonthBooks } from "./MonthBooks";
import { OpenItems } from "./OpenItems";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import type { Brief } from "./Stands";
import { side, TieOut, type Board, type TieRow, type TieStatus } from "./TieOut";
import { amount } from "./workspace";
import type { Close, Paper } from "./WorkpaperView";
import { Xero } from "./Xero";

// A close's Books (docs/design-system.md, Close page): where the books stand in one sentence, then what needs the
// person on this tab and the balance sheet against the trial balance; the rail counts what is found nowhere else and
// opens the tab's other views (the ledger and its captures, the full tie-out, exceptions, changes from last month,
// aging, open items, documents against the ledger, corrections).

export const useBoard = (closeId: string) => useQuery(workspaceQuery<Board>(`/${closeId}/tie-out`));

const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"];
// Rows that say the books tie: ties outright, within the firm's materiality, or once the proposed entries post.
const tied = (status: TieStatus) => status === "ties" || status === "within_materiality" || status === "ties_after_entries";

// BooksLede says where the books stand: they tie, or tie except a few accounts, or how many of the balance-sheet
// accounts tie; and under it which trial balance that is, from where, when it was captured, and capturing again.
export function BooksLede({ close, base }: { close: Close; base: string }) {
  const dates = useDates();
  const read = useBoard(close.id);
  const board = read.data;
  const snapshot = board?.snapshot;
  const files = board?.books_source === "files";
  const rows = board?.rows ?? [];
  const off = rows.filter((row) => !tied(row.status)).length;
  const exact = rows.filter((row) => row.status === "ties").length;
  // "Tie out" is said only of what ties: a difference within materiality and entries still to post are named.
  const tolerated = rows.some((row) => row.status === "within_materiality");
  const pending = rows.some((row) => row.status === "ties_after_entries");
  const sentence = !board
    ? read.isError
      ? "The tie-out could not be read."
      : "The books."
    : board.error
      ? "The trial balance could not be read."
      : !snapshot
        ? files
          ? "No accepted file's books yet."
          : "No trial balance to tie out yet."
        : rows.length === 0
          ? "No balance-sheet account to compare yet."
          : off === 0
            ? `The books tie out${tolerated ? " within materiality" : ""}${pending ? " once the proposed entries post" : ""}.`
            : off <= 3
              ? `The books tie out, except ${words[off]} account${off === 1 ? "" : "s"}.`
              : `${exact} of ${rows.length} balance-sheet accounts tie exactly.`;
  const source = snapshot ? (snapshot.source === "files" ? `Trial balance from the accepted file ${snapshot.file ?? ""}` : `Trial balance #${snapshot.id} from ${snapshot.source === "xero" ? "Xero" : "QuickBooks"}`) : "";
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>
        {board?.error ?? (snapshot ? `${source}, captured ${dates.date(snapshot.captured_at)}${snapshot.stale ? " · out of date" : ""}` : files ? "This month's books come from the client's files" : "Capture this month's trial balance to tie the balance sheet out")}
        {" · "}
        {files ? (
          <Link className="ks-link" to={clientPage(close.entity)}>
            {snapshot ? "The client's files" : "Accept the month's file"}
          </Link>
        ) : (
          <Link className="ks-link" to={`${base}?tab=books&view=ledger`}>
            {snapshot ? "Capture again" : "Capture it"}
          </Link>
        )}
      </p>
    </div>
  );
}

// Each status in the close's words, and its tone.
const statusWords: Record<TieStatus, [string, string]> = {
  ties: ["Ties", ""],
  within_materiality: ["Within materiality", "neutral"],
  ties_after_entries: ["Waits on a draft", "muted"],
  differs: ["Differs", "muted"],
  posting_unknown: ["Posting unknown", "danger"],
  unsupported: ["No support yet", "neutral"],
};
// The filter's groups, in the order the table lists them: what differs first, then what waits, then what ties.
const groups: [string, string, (status: TieStatus) => boolean][] = [
  ["differ", "Differ", (s) => s === "differs" || s === "posting_unknown"],
  ["unsupported", "No support", (s) => s === "unsupported"],
  ["waiting", "Wait on a draft", (s) => s === "ties_after_entries"],
  ["tie", "Tie", (s) => s === "ties" || s === "within_materiality"],
];
const rank = (row: TieRow) => groups.findIndex(([, , test]) => test(row.status));
const money = (value: string | null, currency: string) => (value === null ? "—" : /^-?0(\.0+)?$/.test(value) ? "—" : amount(value, currency));

// BalanceSheet is the tab's one table: each balance-sheet account against the trial balance, what supports it, the
// difference and where it stands, filtered by status. Accounts that tie fold to a line beyond the first few.
function BalanceSheet({ close, board, base }: { close: Close; board: Board | undefined; base: string }) {
  const [filter, setFilter] = useState("all");
  const [all, setAll] = useState(false);
  const currency = board?.currency ?? "USD";
  // An unreadable trial balance is said so, never shown as an empty or zero balance sheet.
  if (!board?.snapshot || board.error)
    return (
      <section className="bare ks-set" aria-labelledby="sheet-title">
        <div className="ks-sec">
          <h2 id="sheet-title">Balance sheet</h2>
        </div>
        {board?.error ? (
          <p role="alert">{board.error}</p>
        ) : (
          <p className="ks-calm">
            <Icon name="book" />
            {board?.books_source === "files" ? (
              <>
                No accepted file's books yet. <Link to={clientPage(close.entity)}>Accept the month's file</Link>
              </>
            ) : (
              <>
                No trial balance yet. <Link to={`${base}?tab=books&view=ledger`}>Capture it from the ledger</Link>
              </>
            )}
          </p>
        )}
      </section>
    );
  const rows = [...board.rows].sort((a, b) => rank(a) - rank(b));
  const counts = new Map(groups.map(([key, , test]) => [key, rows.filter((row) => test(row.status)).length]));
  const active = counts.get(filter) ? filter : "all";
  const chosen = active === "all" ? rows : rows.filter((row) => groups.find(([key]) => key === active)![2](row.status));
  // In "All", every account that does not tie shows, then the first three that do; the rest fold to a line.
  const folded = active === "all" && !all ? chosen.filter((row) => rank(row) === 3).slice(3) : [];
  const shown = chosen.filter((row) => !folded.includes(row));
  return (
    <section className="bare ks-set" aria-labelledby="sheet-title">
      <div className="ks-sec">
        <h2 id="sheet-title">Balance sheet</h2>
        <small>against trial balance #{board.snapshot.id}</small>
        <div className="ks-end">
          <Segmented label="Filter the balance sheet" items={[{ key: "all", label: "All", count: rows.length }, ...groups.filter(([key]) => counts.get(key)).map(([key, label]) => ({ key, label, count: counts.get(key) }))]} value={active} onChange={setFilter} />
        </div>
      </div>
      <div className="ks-table ks-sheet" role="table" aria-labelledby="sheet-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Account</span>
          <span role="columnheader" className="r">
            Trial balance
          </span>
          <span role="columnheader" className="r">
            Supported
          </span>
          <span role="columnheader" className="r">
            Difference
          </span>
          <span role="columnheader">Status</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {shown.map((row) => (
          <div className="ks-tr linked" role="row" key={row.account_id + row.account}>
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={`${base}?tab=books&view=tie-out#tie-${row.account_id}`} title={row.hint}>
                {row.account}
              </Link>
            </span>
            <span className="r" role="cell" title={side(row.balance)}>
              {money(row.balance, currency)}
            </span>
            <span className="r" role="cell">
              {money(row.expected, currency)}
            </span>
            <span className={`r${row.difference?.startsWith("-") ? " ks-neg" : ""}`} role="cell">
              {row.status === "ties" ? "—" : money(row.difference, currency)}
            </span>
            <span role="cell">
              <span className={`pill ${statusWords[row.status][1]}`}>{statusWords[row.status][0]}</span>
              {row.provisional && <small className="ks-mut"> · provisional</small>}
            </span>
            <span role="cell">
              <Icon name="arrow" className="ks-go" />
            </span>
          </div>
        ))}
        {rows.length === 0 && <p className="ks-empty-row">No balance-sheet account has a balance or support.</p>}
      </div>
      {(folded.length > 0 || (all && active === "all" && counts.get("tie")! > 3)) && (
        <p className="ks-table-foot">
          {all ? "" : `${folded.length} more account${folded.length === 1 ? " ties" : "s tie"} · `}
          <button type="button" className="ks-link" onClick={() => setAll(!all)}>
            {all ? "Show fewer" : `Show all ${rows.length}`}
          </button>
        </p>
      )}
      {(board.unmatched.length > 0 || board.unclassified.length > 0) && (
        <Link className="ks-fold wait" to={`${base}?tab=books&view=tie-out`}>
          <Icon name="waiting" />
          {[
            board.unmatched.length ? `${board.unmatched.length} workpaper item${board.unmatched.length === 1 ? " was" : "s were"} not compared` : "",
            board.unclassified.length ? `${board.unclassified.length} account${board.unclassified.length === 1 ? " has" : "s have"} no class, so this may be incomplete` : "",
          ]
            .filter(Boolean)
            .join(" · ")}
          <Icon name="chevron" className="ks-chev" size={14} />
        </Link>
      )}
    </section>
  );
}

// The tab's views beside the balance sheet, each the existing section(s) that hold it.
const views: Record<string, string> = {
  ledger: "The ledger and its captures",
  "tie-out": "The full tie-out",
  exceptions: "Exceptions the agent found",
  changes: "Changes from last month",
  aging: "Receivables and payables aging",
  "open-items": "Open invoices and bills",
  matching: "Documents against the ledger",
  corrections: "Corrections in QuickBooks",
  journals: "Journals to enter in the ledger",
};
// The view a link asks for: by name, or by the anchor of a section inside it (links from the plan, search and the
// exceptions still open the right one).
export function booksView(asked: string | null, hash: string) {
  if (asked && asked in views) return asked;
  if (/^#(quickbooks-ledger|xero|correct-xero-)/.test(hash)) return "ledger";
  if (/^#(correct-(bills|purchases|deposits)-|apply-(payment|vendor_credit)-)/.test(hash)) return "corrections";
  if (hash.startsWith("#tie-")) return "tie-out";
  return "";
}

type ExceptionsRead = { capture?: object | null; items?: unknown[]; balances?: { snapshot?: object | null; items?: unknown[] } };
type FluxRead = { state: string; changes?: unknown[] };
type OpenRead = { state: string; receivables?: unknown[]; payables?: unknown[] };

// Books is the tab's body: on the left what needs the person here and the balance sheet, or one of the tab's views;
// on the right a few figures, the agent and the tab's other views.
export function Books({ close, papers, needs, brief, briefRead, decisionsError, status, base }: { close: Close; papers: Paper[]; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string }) {
  const [params] = useSearchParams();
  const location = useLocation();
  const view = booksView(params.get("view"), location.hash);
  const board = useBoard(close.id).data;
  const exceptions = useQuery(workspaceQuery<ExceptionsRead>(`/${close.id}/quickbooks/exceptions`)).data;
  const flux = useQuery(workspaceQuery<FluxRead>(`/${close.id}/flux`)).data;
  const open = useQuery(workspaceQuery<OpenRead>(`/${close.id}/open-items`)).data;
  const currency = board?.currency ?? "USD";
  const cents = (value: string | null) => {
    if (!value) return 0;
    const [whole, fraction = ""] = value.replace("-", "").split(".");
    return (value.startsWith("-") ? -1 : 1) * (Number(whole) * 100 + Number((fraction + "00").slice(0, 2)));
  };
  const decimal = (value: number) => `${value < 0 ? "-" : ""}${Math.floor(Math.abs(value) / 100)}.${String(Math.abs(value) % 100).padStart(2, "0")}`;
  // Figures come only from a readable trial balance; an unknown posting is counted apart from a measured difference;
  // exceptions are counted only once something was scanned.
  const readable = !!board?.snapshot && !board.error;
  const rows = readable ? board.rows : [];
  const assets = rows.filter((row) => row.class === "Asset").reduce((n, row) => n + cents(row.balance), 0);
  const differ = rows.filter((row) => row.status === "differs");
  const unknown = rows.filter((row) => row.status === "posting_unknown").length;
  const found = exceptions && (exceptions.capture || exceptions.balances?.snapshot) ? (exceptions.capture ? (exceptions.items?.length ?? 0) : 0) + (exceptions.balances?.snapshot ? (exceptions.balances.items?.length ?? 0) : 0) : undefined;
  const moved = flux?.state === "ready" ? (flux.changes?.length ?? 0) : undefined;
  const figures: Figure[] = [
    ...(readable ? ([[amount(decimal(assets), currency).replace(/\.00$/, ""), "total assets"]] as Figure[]) : []),
    ...(differ.length ? ([[amount(decimal(differ.reduce((n, row) => n + Math.abs(cents(row.difference)), 0)), currency).replace(/\.00$/, ""), `differs in ${differ.length === 1 ? "one account" : `${differ.length} accounts`}`, "att"]] as Figure[]) : []),
    ...(unknown ? ([[String(unknown), unknown === 1 ? "account awaiting a posting's outcome" : "accounts awaiting a posting's outcome", "att"]] as Figure[]) : []),
    ...(found !== undefined ? ([[String(found), found === 1 ? "exception the agent found" : "exceptions the agent found"]] as Figure[]) : []),
    ...(moved !== undefined ? ([[String(moved), moved === 1 ? "account moved since last month" : "accounts moved since last month"]] as Figure[]) : []),
  ];
  const openCount = open?.state === "ready" ? (open.receivables?.length ?? 0) + (open.payables?.length ?? 0) : undefined;
  const to = (name: string) => `${base}?tab=books&view=${name}`;
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "books") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=books`}>Balance sheet</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "ledger" && (
                <>
                  <MonthBooks key={`books-${close.id}`} closeId={close.id} entity={close.entity} period={close.period} />
                  <Xero key={`xero-${close.id}`} closeId={close.id} entity={close.entity} period={close.period} />
                </>
              )}
              {view === "tie-out" && <TieOut key={`tie-out-${close.id}`} closeId={close.id} />}
              {view === "journals" && <Journals key={`journals-${close.id}`} closeId={close.id} />}
              {view === "exceptions" && <Exceptions key={`exceptions-${close.id}`} closeId={close.id} />}
              {view === "changes" && <Flux key={`flux-${close.id}`} closeId={close.id} />}
              {view === "aging" && <Aging key={`aging-${close.id}`} closeId={close.id} open />}
              {view === "open-items" && <OpenItems key={`open-items-${close.id}`} closeId={close.id} open />}
              {view === "matching" && <Matching key={`matching-${close.id}`} closeId={close.id} open />}
              {view === "corrections" && (
                <>
                  <BillCorrection closeId={close.id} />
                  <BillCorrection closeId={close.id} kind="purchases" />
                  <BillCorrection closeId={close.id} kind="deposits" />
                  <CreditApplications closeId={close.id} />
                </>
              )}
            </div>
          </>
        ) : (
          <>
            <NeedsYou closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            <BalanceSheet close={close} board={board} base={base} />
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the books">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Books"
          links={[
            { label: views.exceptions, note: found ? String(found) : undefined, to: to("exceptions") },
            { label: views.changes, note: moved ? `${moved} account${moved === 1 ? "" : "s"}` : undefined, to: to("changes") },
            { label: views.aging, to: to("aging") },
            { label: views["open-items"], note: openCount ? `${openCount} open` : undefined, to: to("open-items") },
            { label: views.matching, to: to("matching") },
            { label: views["tie-out"], note: board?.unmatched.length ? `${board.unmatched.length} not compared` : undefined, to: to("tie-out") },
            { label: views.ledger, note: board?.snapshot ? `#${board.snapshot.id}` : undefined, to: to("ledger") },
            { label: views.corrections, to: to("corrections") },
            // A month on files has no ledger to post to: its approved entries are journals to enter.
            ...(board?.snapshot?.source === "files" ? [{ label: views.journals, to: to("journals") }] : []),
          ]}
        />
      </aside>
    </div>
  );
}
