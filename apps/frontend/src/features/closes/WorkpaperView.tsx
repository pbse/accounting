import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import type { AccrualWorkpaper } from "../../../../../packages/contracts/accrual";
import type { PrepaidWorkpaper } from "../../../../../packages/contracts/prepaid";
import type { ReconciliationWorkpaper } from "../../../../../packages/contracts/reconciliation";
import type { SettlementWorkpaper } from "../../../../../packages/contracts/settlement";
import type { LoanWorkpaper } from "../../../../../packages/contracts/loan";
import type { OwnerWorkpaper } from "../../../../../packages/contracts/owner";
import { AccrualResult } from "../accruals/Prepare";
import { EffortTracker } from "./Effort";
import { Icon } from "../dashboard/Icon";
import { ReconciliationResult } from "../reconciliation/Prepare";
import { SettlementResult } from "../settlements/Prepare";
import { LoanResult } from "../loans/Prepare";
import { OwnerResult } from "../owners/Prepare";
import type { Source } from "./Evidence";
import type { Plan } from "./Planning";
import { Posting, ProductionPosting, XeroPosting } from "./Posting";
import { ReviewDecision } from "./ReviewDecision";
import { StandingApproval } from "./StandingApproval";
import { VendorCredit } from "./VendorCredit";
import { scheduleTerms } from "../prepaids/MethodFields";
import { money, workspaceRequest } from "./workspace";
import { citedLines, citedPage } from "./citation";
import { Comments, type PickedLine } from "./Comments";

export type Profile = { currency: string; fiscal_year_start: number; state: "detected" | "none" | "unknown" | "conflict" };
export type Close = { id: string; entity: string; period: string; basis?: "cash" | "modified_cash" | "accrual"; basis_scope?: "client" | "firm"; profile?: Profile } & Plan;
type Saved<K, P> = { kind: K; paper: P & { sources?: Source[]; revises?: string; blockers?: string[] } };
export type Paper = { id: string; created_at: string; review_state: string } & (
  | Saved<"prepaid", PrepaidWorkpaper>
  | Saved<"accrual", AccrualWorkpaper>
  | Saved<"reconciliation", ReconciliationWorkpaper>
  | Saved<"settlement", SettlementWorkpaper>
  | Saved<"loan", LoanWorkpaper>
  | Saved<"owner", OwnerWorkpaper>
);

export const paperLabels = { prepaid: "Prepaid schedule", accrual: "Accrued expense", reconciliation: "Statement reconciliation", settlement: "Stripe settlement", loan: "Loan schedule", owner: "Owner reclassification" };
export const paperLabel = (p: Paper) => (p.kind === "prepaid" && p.paper.input.schedule ? `${scheduleTerms[p.paper.input.schedule].name} schedule` : paperLabels[p.kind]);
export const paperPaths = { prepaid: "prepaids", accrual: "accruals", reconciliation: "reconciliations", settlement: "settlements", loan: "loans", owner: "owners" };
const reviewLabels: Record<string, [string, string]> = {
  pending: ["Awaiting review", "pill muted"],
  approved: ["Approved", "pill"],
  changes_requested: ["Changes requested", "pill danger"],
  source_changed: ["Evidence changed", "pill danger"],
};
const roleLabels: Record<string, string> = {
  invoice: "Invoice",
  support: "Support",
  ledger: "Ledger",
  statement: "Statement",
  processor_report: "Stripe export",
  activity_report: "Stripe activity report",
  bank: "Bank CSV",
  procedure: "Approved procedure",
  client_reply: "Client reply",
};

export function ReviewPill({ state }: { state: string }) {
  const [label, className] = reviewLabels[state] ?? [state.replaceAll("_", " "), "pill neutral"];
  return <span className={className}>{label}</span>;
}

export function needsAttention(paper: Paper) {
  return paper.review_state === "changes_requested" || paper.review_state === "source_changed" || (paper.paper.blockers?.length ?? 0) > 0;
}

export function readyForReview(paper: Paper) {
  return paper.review_state === "pending" && (paper.paper.sources?.length ?? 0) > 0 && (paper.paper.blockers?.length ?? 0) === 0;
}

export function useClose(id: string | undefined) {
  const [close, setClose] = useState<Close>();
  const [papers, setPapers] = useState<Paper[]>([]);
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const generation = useRef(0);
  const load = useCallback(async () => {
    if (!id) return;
    const current = ++generation.current;
    setLoading(true);
    setError("");
    try {
      const data = await workspaceRequest(`/${encodeURIComponent(id)}`);
      if (current !== generation.current) return;
      setClose(data.close);
      setPapers(data.workpapers ?? []);
      setMore(data.has_more);
    } catch (e) {
      if (current === generation.current) setError(e instanceof Error ? e.message : "Could not open the close.");
    } finally {
      if (current === generation.current) setLoading(false);
    }
  }, [id]);
  useEffect(() => {
    void load();
    return () => {
      generation.current++;
    };
  }, [load]);
  return { close, papers, more, error, loading, reload: load };
}

function EvidencePane({ closeId, sources, onPickLine }: { closeId: string; sources: Source[]; onPickLine: (line: PickedLine) => void }) {
  const [active, setActive] = useState(0);
  const [texts, setTexts] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [original, setOriginal] = useState("");
  const shown = useRef("");
  const mounted = useRef(false);
  shown.current = sources[active]?.id ?? "";
  useEffect(() => {
    mounted.current = true;
    return () => void (mounted.current = false);
  }, []);
  useEffect(() => () => URL.revokeObjectURL(original), [original]);
  useEffect(() => setOriginal(""), [active]);
  const firstCited = useRef<HTMLDivElement>(null);
  const source = sources[active];
  const text = source ? texts[source.id] : undefined;
  useEffect(() => {
    if (!source || text !== undefined) return;
    let live = true;
    setError("");
    workspaceRequest(`/${closeId}/evidence/${source.id}/text`, undefined, true)
      .then((blob: Blob) => blob.text())
      .then((value) => live && setTexts((current) => ({ ...current, [source.id]: value })))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Could not open source."));
    return () => {
      live = false;
    };
  }, [closeId, source, text]);
  useEffect(() => {
    firstCited.current?.scrollIntoView({ block: "center" });
  }, [text]);
  if (!source)
    return (
      <section className="evidence-pane">
        <header>
          <h2>Evidence</h2>
        </header>
        <p className="empty">No linked evidence. Prepare this work from uploaded evidence before approval.</p>
      </section>
    );
  const cited = text === undefined ? undefined : citedLines(source.location, text);
  const pdf = source.name.toLowerCase().endsWith(".pdf");
  return (
    <section className="evidence-pane" aria-label="Evidence as received">
      <header>
        <h2>Evidence · as received</h2>
        <div className="segmented" role="group" aria-label="Linked sources">
          {sources.map((item, index) => (
            <button type="button" key={`${item.id}-${item.role}`} aria-pressed={index === active} onClick={() => setActive(index)}>
              {roleLabels[item.role] ?? item.role.replaceAll("_", " ")}
            </button>
          ))}
        </div>
      </header>
      {original && <iframe title={`${source.name}, page ${citedPage(source.location)}`} src={`${original}#page=${citedPage(source.location)}`} style={{ width: "100%", height: "32rem", border: 0 }} />}
      {error ? (
        <p role="alert" className="empty">{error}</p>
      ) : text === undefined ? (
        <p role="status" className="empty">Opening {source.name}…</p>
      ) : (
        <div className="source-text" tabIndex={0} aria-label={`${source.name} extracted text`}>
          {text.split("\n").map((line, index) => {
            const number = index + 1;
            const highlighted = !!cited && number >= cited[0] && number <= cited[1];
            return (
              <div key={index} ref={highlighted && number === cited[0] ? firstCited : undefined} className={highlighted ? "source-line cited" : "source-line"}>
                <button type="button" className="line-number" title="Comment on this line" onClick={() => onPickLine({ sourceId: source.id, name: source.name, line: number })}>{number}</button>
                <span>{line || " "}</span>
              </div>
            );
          })}
        </div>
      )}
      <div className="evidence-meta">
        <strong>{source.name}</strong>
        <span className="chip">Cited: {source.location}</span>
        {pdf && (
          <button
            type="button"
            onClick={async () => {
              if (original) return setOriginal("");
              const asked = source.id;
              try {
                const url = URL.createObjectURL(await workspaceRequest(`/${closeId}/evidence/${asked}`, undefined, true));
                // A late answer for a source no longer shown is dropped.
                if (mounted.current && shown.current === asked) setOriginal(url);
                else URL.revokeObjectURL(url);
              } catch (e) {
                if (mounted.current && shown.current === asked) setError(e instanceof Error ? e.message : "The original is unavailable.");
              }
            }}
          >
            {original ? "Hide original" : `View original, page ${citedPage(source.location)}`}
          </button>
        )}
        <span>revision {source.digest.slice(0, 12)}</span>
        <button
          type="button"
          onClick={async () => {
            try {
              const blob = await workspaceRequest(`/${closeId}/evidence/${source.id}`, undefined, true);
              const url = URL.createObjectURL(blob);
              const link = document.createElement("a");
              link.href = url;
              link.download = source.name;
              link.click();
              setTimeout(() => URL.revokeObjectURL(url), 1000);
            } catch (e) {
              setError(e instanceof Error ? e.message : "Download failed.");
            }
          }}
        >
          Download original
        </button>
      </div>
    </section>
  );
}

type NativeAccount = { account_id: string; account: string; reconciled: number; unreconciled: number; unknown: number };

// Xero's own reconciled flags for this account, beside (not confirming) the reviewer's cited native status.
function NativeFlags({ closeId, account }: { closeId: string; account: string }) {
  const [text, setText] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/native-reconciliation`)
      .then((out: { status: string; reason?: string; captured_at?: string; accounts?: NativeAccount[] }) => {
        if (!live) return;
        if (out.status !== "read") return setText(`Provider flags not read: ${out.reason ?? ""}`);
        const named = (out.accounts ?? []).filter((a) => a.account.trim().toLowerCase() === account.trim().toLowerCase());
        if (named.length !== 1) return setText(`No single Xero bank account is named ${account}, so Xero's flags are not shown.`);
        const a = named[0];
        setText(`Xero flagged ${a.reconciled} of ${a.reconciled + a.unreconciled + a.unknown} captured records for ${a.account} as reconciled at ${new Date(out.captured_at ?? "").toLocaleString()}${a.unknown ? ` (${a.unknown} without a flag)` : ""}; native account completion and statement settlement were not verified.`);
      })
      .catch(() => live && setText("Provider flags are unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, account]);
  return text ? <p className="note">{text}</p> : null;
}

export function WorkpaperResult({ close, saved }: { close: Close; saved: Paper }) {
  const posting = (
    <>
      <ProductionPosting closeId={close.id} paperId={saved.id} />
      <XeroPosting closeId={close.id} paperId={saved.id} />
    </>
  );
  if (saved.kind === "settlement") return <><SettlementResult result={saved.paper} />{saved.paper.proposal && saved.paper.blockers.length === 0 && posting}</>;
  if (saved.kind === "accrual") return <><AccrualResult result={saved.paper} />{saved.paper.proposal && posting}</>;
  if (saved.kind === "loan") return <><LoanResult result={saved.paper} />{saved.paper.proposals.length > 0 && saved.paper.blockers.length === 0 && posting}</>;
  if (saved.kind === "owner") return <><OwnerResult result={saved.paper} />{saved.paper.proposal && posting}</>;
  if (saved.kind === "reconciliation") return <><ReconciliationResult result={saved.paper} /><NativeFlags closeId={close.id} account={saved.paper.input.account} /></>;
  const paper = saved.paper;
  const terms = scheduleTerms[paper.input.schedule ?? ""];
  return (
    <>
      <p>Source: {paper.input.source_reference} · user-supplied, unverified.</p>
      {paper.carry_forward ? (
        <section>
          <h3>Carried schedule · fresh review required</h3>
          <p>
            From <Link to={`/dashboard/closes/${paper.carry_forward.close_id}`}>{paper.carry_forward.period} · draft #{paper.carry_forward.paper_id}</Link>. Opening scheduled balance: {money(paper.carry_forward.opening_balance, paper.input.currency)}.
          </p>
          <p>
            Schedule only: this is not a verified ledger opening balance. No journal is proposed. Review current contracts, credits, account mappings and prior postings before accounting action. To correct the schedule, revise and review its predecessor, then carry that new version forward.
          </p>
        </section>
      ) : (
        <p>
          Method: {paper.input.method}{paper.input.factor && ` × ${paper.input.factor}`}{paper.input.convention && `, ${paper.input.convention}`}{paper.input.accountant_rule_reference && ` under ${paper.input.accountant_rule_reference}`}, final-month rounding. {paper.input.booked_balance ? `${terms.booked} ${money(paper.input.booked_balance, paper.input.currency)} · ${paper.input.booking_reference}.` : terms.blank}{paper.input.salvage_value ? ` Salvage value ${money(paper.input.salvage_value, paper.input.currency)}.` : ""} {paper.input.credit_amount ? `Credit ${money(paper.input.credit_amount, paper.input.currency)} effective ${paper.input.credit_period} · ${paper.input.credit_reference}.` : ""} {paper.input.cancellation_method ? `Cancellation ${paper.input.cancellation_method} effective ${paper.input.cancellation_period} · ${paper.input.cancellation_reference}.` : ""}
        </p>
      )}
      <p>
        {terms.recognized} this period: {money(paper.period_expense, paper.input.currency)}; to date: {money(paper.recognized_expense, paper.input.currency)}; {terms.remaining.toLowerCase()}: {money(paper.prepaid_balance, paper.input.currency)}.
      </p>
      <p>
        {paper.proposal
          ? `Draft adjustment ${paper.proposal.date}: debit ${paper.proposal.debit_account}, credit ${paper.proposal.credit_account}, ${money(paper.proposal.amount, paper.input.currency)}.`
          : "No reclassification proposed."}
        {paper.disposal?.map((e) => ` Disposal ${e.date}, book by hand: debit ${e.debit_account}, credit ${e.credit_account}, ${money(e.amount, paper.input.currency)}.`)}
      </p>
      <table>
        <caption>Saved {terms.name.toLowerCase()} schedule</caption>
        <thead>
          <tr>
            <th>Month</th>
            <th>{terms.recognized} {paper.input.currency}</th>
            <th>Remaining {paper.input.currency}</th>
            <th>Credit {paper.input.currency}</th>
          </tr>
        </thead>
        <tbody>
          {paper.schedule.map((row) => (
            <tr key={row.period}>
              <th>{row.period}</th>
              <td>{row.expense}</td>
              <td>{row.remaining}</td>
              <td>{row.credit ?? "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {!paper.carry_forward && !paper.input.schedule && paper.input.method === "equal_full_months_v1" && !paper.input.credit_amount && !paper.input.booked_balance && !paper.input.cancellation_method && (
        <>
          <Posting closeId={close.id} paperId={saved.id} />
          <ProductionPosting closeId={close.id} paperId={saved.id} />
          <XeroPosting closeId={close.id} paperId={saved.id} />
        </>
      )}
      {!paper.input.schedule && <VendorCredit closeId={close.id} paperId={saved.id} paper={paper} sources={paper.sources ?? []} approved={saved.review_state === "approved"} />}
    </>
  );
}

export default function WorkpaperView() {
  const { id, paperId } = useParams();
  const { close, papers, more, error, loading, reload } = useClose(id);
  const [older, setOlder] = useState<Paper>();
  const [picked, setPicked] = useState<PickedLine & { paper: string }>();
  const [olderError, setOlderError] = useState("");
  const listed = papers.find((paper) => paper.id === paperId);
  useEffect(() => {
    setOlder(undefined);
    setOlderError("");
    if (loading || listed || !close) return;
    let live = true;
    workspaceRequest(`/${close.id}/workpapers/${paperId}?record=1`)
      .then((record: Paper) => live && setOlder(record))
      .catch((e) => live && setOlderError(e instanceof Error ? e.message : "Workpaper unavailable."));
    return () => {
      live = false;
    };
  }, [close, paperId, listed, loading]);
  if (loading && !close) return <p role="status">Opening workpaper…</p>;
  if (error || olderError) return <p role="alert">{error || olderError}</p>;
  const saved = listed ?? older;
  if (!close || !saved) return <p role="status">Opening workpaper…</p>;
  const queue = papers.filter(readyForReview);
  const position = queue.findIndex((paper) => paper.id === saved.id);
  const next = queue[position + 1] ?? (position === -1 ? queue[0] : undefined);
  const previous = position > 0 ? queue[position - 1] : undefined;
  const blockers = saved.paper.blockers ?? [];
  return (
    <>
      <EffortTracker closeId={close.id} activity="review" />
      <Link className="back-link" to={`/dashboard/closes/${close.id}?tab=workpapers`}>
        <Icon name="back" size={15} /> {close.entity} · {close.period}
      </Link>
      <div className="page-title">
        <div>
          <span className="kpi-icon" aria-hidden="true">
            <Icon name="file" />
          </span>
          <span>
            <h1>
              {paperLabel(saved)} · draft #{saved.id}
            </h1>
            <p>
              <ReviewPill state={saved.review_state} /> Prepared {new Date(saved.created_at).toLocaleString()}
              {saved.paper.revises ? ` · recalculated from draft #${saved.paper.revises}` : ""}
            </p>
          </span>
        </div>
        <div className="title-actions">
          {previous && (
            <Link className="button" to={`/dashboard/closes/${close.id}/workpapers/${previous.id}`}>
              Previous in queue
            </Link>
          )}
          {next && (
            <Link className="button" to={`/dashboard/closes/${close.id}/workpapers/${next.id}`}>
              Next to review{position === -1 ? "" : ` · ${queue.length - position - 1} left`}
            </Link>
          )}
          {!(saved.kind === "prepaid" && saved.paper.carry_forward) && (
            <Link className="button" to={`/dashboard/${paperPaths[saved.kind]}/new?close=${close.id}&revise=${saved.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}${saved.kind === "prepaid" && saved.paper.input.schedule ? `&schedule=${saved.paper.input.schedule}` : ""}`}>
              Revise and recalculate
            </Link>
          )}
        </div>
      </div>
      {more && <p className="note">Review navigation covers the latest 100 drafts in this close.</p>}
      <div className="canvas">
        <EvidencePane key={saved.id} closeId={close.id} sources={saved.paper.sources ?? []} onPickLine={(line) => setPicked({ ...line, paper: saved.id })} />
        <div className="stack">
          {blockers.length > 0 && (
            <section role="status">
              <h2>Blockers keep this draft from approval</h2>
              <ul>
                {blockers.map((blocker) => (
                  <li key={blocker}>{blocker}</li>
                ))}
              </ul>
            </section>
          )}
          <section aria-label="Prepared calculation">
            <h2>Prepared calculation</h2>
            <WorkpaperResult key={saved.id} close={close} saved={saved} />
          </section>
          <ReviewDecision
            key={saved.id}
            closeId={close.id}
            paperId={saved.id}
            onChange={() => {
              void reload();
              if (older)
                void workspaceRequest(`/${close.id}/workpapers/${saved.id}?record=1`).then((record: Paper) =>
                  setOlder((current) => (current?.id === record.id ? record : current)),
                );
            }}
            autoLoad
          />
          <Comments key={`comments-${saved.id}`} closeId={close.id} paperId={saved.id} fields={Object.keys((saved.paper as { input?: object }).input ?? {})} picked={picked?.paper === saved.id ? picked : undefined} canComment={saved.review_state !== "approved"} />
          {(saved.kind === "prepaid" || saved.kind === "loan" || (saved.kind === "accrual" && saved.paper.input.method === "fixed_recurring_incurred_v1")) && (
            <StandingApproval key={`standing-${saved.id}-${saved.review_state}`} closeId={close.id} paperId={saved.id} kind={saved.kind} approved={saved.review_state === "approved"} />
          )}
          <details>
            <summary>Original inputs and full calculation</summary>
            <pre>{JSON.stringify(saved.paper, null, 2)}</pre>
          </details>
        </div>
      </div>
    </>
  );
}
