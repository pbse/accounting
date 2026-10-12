import { type ReactNode, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { workspaceQuery } from "../../data/queries";
import { monthName, monthShort, useDates } from "../../data/time";
import { Budget, type Report as BudgetReport } from "./Budget";
import type { Status } from "./Goal";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import { downloadPublication, Publication, Reports, type Line, type Pack, type Published, type Section } from "./Reports";
import type { Brief } from "./Stands";
import type { Close, Paper } from "./WorkpaperView";
import { amount } from "./workspace";

// A close's Reports (docs/design-system.md, Close page): where the month's management pack stands in one sentence, then
// the decisions on this tab and one statement at a time against last month, or against the budget; the rail gives the
// month's headline figures and opens the tab's other views (the full pack, published packs, the budget).

type Ready = Extract<Pack, { state: "ready" }>;
const usePack = (closeId: string) => useQuery(workspaceQuery<Pack>(`/${closeId}/management-pack`));
const usePublications = (closeId: string) => useQuery(workspaceQuery<{ publications: Published[] }>(`/${closeId}/management-pack/publications`));
const useBudget = (closeId: string) => useQuery(workspaceQuery<BudgetReport>(`/${closeId}/budget`));

// What keeps a pack from being published, the first that applies, in the lede's words; "" when it can be published.
// These are the server's own conditions: a current capture, a complete pack and the sign-off of every month shown.
function until(pack: Ready) {
  if (pack.stale) return "the month is captured again";
  if (pack.profit_and_loss_unavailable) return "the month's activity can be read";
  if (Number(pack.balance_sheet.difference) !== 0) return "the balance sheet balances";
  if ([...pack.profit_and_loss.sections, ...pack.balance_sheet.sections].some((section) => section.name === "Unclassified")) return "every account is classified";
  // Xero and a month's files have no statement of their own, so the firm imports one; QuickBooks's comes with its capture.
  if ("unavailable" in pack.cash_flow) return pack.source === "quickbooks" ? "the ledger's cash-flow statement can be read" : "the firm's cash-flow statement is imported";
  if (!pack.cash_flow.reconciled) return "the cash flow ties to the books";
  if (pack.overall !== "signed_off") return pack.status === "signed_off" && pack.prior ? `${monthName(pack.prior.period)}'s sign-off stands` : "the month is signed off";
  return "";
}

const month = (period: string) => monthName(period).replace(/ \d{4}$/, "");

// ReportsLede is the tab's sentence: whether the month's pack is published, needs a correction, can be published, or
// what it waits on. Under it: the pack's basis, what it is compared with and its latest version. Versions that could not
// be read are said so, never taken for none.
export function ReportsLede({ closeId, period }: { closeId: string; period: string }) {
  const dates = useDates();
  const read = usePack(closeId);
  const versions = usePublications(closeId);
  const latest = versions.data?.publications[0];
  const pack = read.data;
  const sentence = !pack
    ? read.isError
      ? "The management pack could not be read."
      : "The month's management pack."
    : pack.state === "waiting"
      ? "The management pack waits on the month's books."
      : latest?.state === "needs_correction"
        ? `The books changed after version ${latest.version} was published.`
        : latest?.state === "signoff_lapsed"
          ? `Version ${latest.version}'s sign-off no longer stands.`
          : latest?.state === "current"
            ? `${month(period)}'s pack is published, version ${latest.version}.`
            : until(pack)
              ? `${month(period)}'s pack is provisional until ${until(pack)}.`
              : !versions.data
                ? versions.isError
                  ? `${month(period)}'s pack can be published; its published versions could not be read.`
                  : `${month(period)}'s management pack.`
                : `${month(period)}'s pack is ready to publish.`;
  const facts =
    pack?.state === "ready"
      ? ["Management pack", `${pack.basis} basis`, pack.prior ? `against ${monthName(pack.prior.period)}` : "no comparison", latest ? `version ${latest.version} published ${dates.date(latest.published_at)}` : versions.data ? "not published yet" : versions.isError ? "published versions not read" : ""].filter(Boolean)
      : pack?.state === "waiting"
        ? [pack.reason]
        : [];
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      {facts.length > 0 && <p>{facts.join(" · ")}</p>}
    </div>
  );
}

const statements = [
  ["pl", "Profit and loss"],
  ["bs", "Balance sheet"],
  ["cf", "Cash flow"],
  ["budget", "Budget"],
] as const;

// Statement is the tab's one table: a statement's lines against last month, or the month against its budget; the cash
// flow is the month's alone, having no comparison. A figure whose ledger rows can be read opens them in the full pack.
// versions is undefined until the published versions are read: nothing is offered on them before.
function Statement({ closeId, pack, budget, budgetError, versions, base }: { closeId: string; pack: Ready; budget: BudgetReport | undefined; budgetError: string; versions: Published[] | undefined; base: string }) {
  const [shown, setShown] = useState("pl");
  const [error, setError] = useState("");
  const latest = versions?.[0];
  const single = shown === "cf";
  const show = (value: string | null) => (value === null ? "—" : amount(value, pack.currency));
  const moved = (value: string | null) => (value === null || Number(value) === 0 ? "—" : `${Number(value) > 0 ? "+" : ""}${amount(value, pack.currency)}`);
  const to = (name: string) => `${base}?tab=reports&view=${name}`;
  const drill = pack.source === "quickbooks" || pack.source === "files";
  const line = (l: Line, key: string, strong = false) => {
    const linked = drill && !!l.account_id;
    return (
      <div className={`ks-tr${linked ? " linked" : ""}${strong ? " ks-total" : ""}`} role="row" key={key}>
        <span className="ks-who" role="cell">
          {linked ? (
            <Link className="ks-rowlink" to={`${to("pack")}&account=${encodeURIComponent(l.account_id!)}#figure-ledger`}>
              {l.name}
            </Link>
          ) : (
            l.name
          )}
        </span>
        <span className="r" role="cell">
          {show(l.amount)}
        </span>
        <span className="r ks-mut" role="cell">
          {!single && show(l.prior)}
        </span>
        <span className="r" role="cell">
          {!single && moved(l.change)}
        </span>
        <span role="cell">{linked && <Icon name="arrow" className="ks-go" />}</span>
      </div>
    );
  };
  const group = (name: string) => (
    <div className="ks-tr ks-group" role="row" key={`group-${name}`}>
      <span role="cell">{name}</span>
    </div>
  );
  const sections = (list: Section[], after: Line[]) => [...list.flatMap((section) => [group(section.name), ...section.lines.map((l, i) => line(l, `${section.name}-${l.account_id ?? i}`)), line(section.total, `${section.name}-total`, true)]), ...after.map((l) => line(l, `after-${l.name}`, true))];
  const empty = (text: ReactNode) => (
    <div className="ks-tr" role="row">
      <p className="ks-empty-row" role="cell">
        {text}
      </p>
    </div>
  );
  const flow = pack.cash_flow;
  let rows: ReactNode;
  if (shown === "pl") rows = pack.profit_and_loss_unavailable ? empty(`The month's activity is unavailable: ${pack.profit_and_loss_unavailable}. It is never estimated.`) : sections(pack.profit_and_loss.sections, pack.profit_and_loss.measures);
  else if (shown === "bs") rows = sections(pack.balance_sheet.sections, [pack.balance_sheet.earnings, pack.balance_sheet.total_assets, pack.balance_sheet.total_liabilities, pack.balance_sheet.total_equity]);
  else if (shown === "cf")
    rows =
      "unavailable" in flow
        ? empty(
            <>
              {flow.unavailable} {pack.open && (pack.source === "xero" || pack.source === "files") && <Link to={to("pack")}>Import the firm's statement</Link>}
            </>,
          )
        : sections(flow.sections, [flow.net_change, flow.beginning, flow.ending]);
  else if (!budget) rows = empty(budgetError || "Reading the budget…");
  else if (budget.state === "no_budget")
    rows = empty(
      <>
        No approved budget covers {monthName(pack.period)}. <Link to={to("budget")}>Import a budget</Link>
      </>,
    );
  else if (budget.state === "waiting") rows = empty(budget.reason);
  else {
    const figure = (name: string, f: NonNullable<BudgetReport["totals"]>["net_month"], key: string, strong = false, unbudgeted = false) => (
      <div className={`ks-tr${strong ? " ks-total" : ""}`} role="row" key={key}>
        <span className="ks-who" role="cell">
          {name}
        </span>
        <span className="r" role="cell">
          {show(f.actual)}
        </span>
        <span className="r ks-mut" role="cell">
          {unbudgeted ? "Not budgeted" : show(f.budget)}
        </span>
        <span className={`r${f.material && !f.favorable ? " ks-neg" : ""}`} role="cell">
          {moved(f.variance)}
        </span>
        <span role="cell" />
      </div>
    );
    const by = (kind: string) => (budget.rows ?? []).filter((row) => row.class === kind).map((row) => figure(row.name, row.month, row.account_id, false, row.unbudgeted));
    const totals = budget.totals;
    rows = [group("Revenue"), ...by("Revenue"), group("Expenses"), ...by("Expense"), ...(totals ? [figure("Income", totals.income_month, "income", true), figure("Expenses", totals.expense_month, "expenses", true), figure("Net income", totals.net_month, "net", true)] : [])];
  }
  const blocked = until(pack);
  const head = shown === "budget" ? ["Actual", budget?.baseline ? `Budget · ${budget.baseline.name}` : "Budget", "Difference"] : single ? [monthShort(pack.period), "", ""] : [monthShort(pack.period), pack.prior ? monthShort(pack.prior.period) : "No comparison", "Change"];
  return (
    <section className="bare ks-set" aria-labelledby="statement-title">
      <div className="ks-sec">
        <h2 id="statement-title">Statement</h2>
        <div className="ks-end">
          <Segmented label="Choose a statement" items={statements.map(([key, label]) => ({ key, label }))} value={shown} onChange={setShown} />
          {latest && (
            <button type="button" title={`Version ${latest.version}, as published`} onClick={() => void downloadPublication(closeId, latest, "pdf").catch((e) => setError(e instanceof Error ? e.message : "The export is unavailable."))}>
              PDF
            </button>
          )}
          {latest?.state === "current" ? null : blocked || !versions ? (
            <button type="button" disabled title={blocked ? `Publish once ${blocked}` : "The published versions are not read yet"}>
              Publish
            </button>
          ) : (
            <Link className="button" to={to("publish")}>
              {latest ? "Publish a correction" : "Publish"}
            </Link>
          )}
        </div>
      </div>
      {error && <p role="alert">{error}</p>}
      <div className="ks-table ks-statement" role="table" aria-labelledby="statement-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Line</span>
          {head.map((label, i) => (
            <span role="columnheader" className="r" key={i}>
              {label}
            </span>
          ))}
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {rows}
      </div>
      {shown === "bs" && Number(pack.balance_sheet.difference) !== 0 && <p role="alert">Assets differ from liabilities and equity by {show(pack.balance_sheet.difference)}.</p>}
      {shown === "cf" && !("unavailable" in flow) && !flow.reconciled && <p role="alert">Does not tie to the books. {flow.differences.join(" ")}</p>}
    </section>
  );
}

// The month's headline figures: revenue and its change, net income, book cash at month end and net income against the
// budget, each only when the pack holds it.
function figuresOf(pack: Pack | undefined, budget: BudgetReport | undefined): Figure[] {
  if (pack?.state !== "ready") return [];
  const show = (value: string) => amount(value, pack.currency);
  const income = pack.profit_and_loss.sections.find((section) => section.name === "Income")?.total;
  const net = pack.profit_and_loss.measures.at(-1);
  const wc = pack.working_capital;
  const cash = !("unavailable" in wc) ? wc.book_cash.amount : !("unavailable" in pack.cash_flow) ? pack.cash_flow.book_cash : null;
  const change = (l: Line) => (!l.prior || !l.change || Number(l.prior) === 0 ? "" : Number(l.change) === 0 ? ", unchanged" : `, ${Number(l.change) > 0 ? "up" : "down"} ${Math.round(Math.abs(Number(l.change) / Number(l.prior)) * 100)}%`);
  const versus = budget?.state === "ready" ? budget.totals?.net_month : undefined;
  return [
    ...(income ? ([[show(income.amount), `revenue${change(income)}`]] as Figure[]) : []),
    ...(net ? ([[show(net.amount), pack.overall === "signed_off" ? "net income" : "net income, provisional"]] as Figure[]) : []),
    ...(cash !== null ? ([[show(cash), "cash at month end"]] as Figure[]) : []),
    ...(versus ? ([[`${Number(versus.variance) > 0 ? "+" : ""}${show(versus.variance)}`, "net income against budget", versus.material && !versus.favorable ? "att" : undefined]] as Figure[]) : []),
  ];
}

const views: Record<string, string> = { pack: "The full pack", publish: "Published packs", budget: "Budget against actual" };

// ReportsTab is the tab's body: on the left what needs the person here and one statement, or one of the tab's views; on
// the right the month's figures, the agent and the tab's other views.
export function ReportsTab({ close, papers, needs, brief, briefRead, decisionsError, status, base }: { close: Close; papers: Paper[]; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string }) {
  const [params] = useSearchParams();
  const asked = params.get("view");
  const view = asked && asked in views ? asked : "";
  const packRead = usePack(close.id);
  const pack = packRead.data;
  const versions = usePublications(close.id);
  const latest = versions.data?.publications[0];
  const budgetRead = useBudget(close.id);
  const budget = budgetRead.data;
  const to = (name: string) => `${base}?tab=reports&view=${name}`;
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "reports") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=reports`}>Reports</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "pack" && <Reports closeId={close.id} account={params.get("account") ?? ""} />}
              {view === "publish" && <Publication closeId={close.id} pack={pack} />}
              {view === "budget" && <Budget closeId={close.id} open />}
            </div>
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            {!pack ? (
              packRead.isError ? <p role="alert">{packRead.error.message || "The management pack is unavailable."}</p> : <p role="status">Preparing the management pack…</p>
            ) : pack.state === "waiting" ? (
              <section className="bare ks-set" aria-labelledby="statement-title">
                <div className="ks-sec">
                  <h2 id="statement-title">Statement</h2>
                </div>
                <p className="ks-calm">
                  <Icon name="book" />
                  {pack.reason}
                </p>
              </section>
            ) : (
              <Statement closeId={close.id} pack={pack} budget={budget} budgetError={budgetRead.isError ? budgetRead.error.message || "The budget comparison is unavailable." : ""} versions={versions.data?.publications} base={base} />
            )}
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the reports">
        <Glance figures={figuresOf(pack, budget)} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Reports"
          links={[
            { label: views.pack, to: to("pack") },
            { label: views.publish, note: latest ? `version ${latest.version}` : versions.data ? "none yet" : undefined, to: to("publish") },
            { label: views.budget, note: budget?.baseline?.name ?? (budget?.state === "no_budget" ? "no budget" : undefined), to: to("budget") },
          ]}
        />
      </aside>
    </div>
  );
}
