import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams } from "react-router";
import type { AccrualWorkpaper } from "../../../../../packages/contracts/accrual";
import type { PrepaidWorkpaper } from "../../../../../packages/contracts/prepaid";
import type { ReconciliationWorkpaper } from "../../../../../packages/contracts/reconciliation";
import type { SettlementWorkpaper } from "../../../../../packages/contracts/settlement";
import { AccrualResult } from "../accruals/Prepare";
import { Icon } from "../dashboard/Icon";
import { ReconciliationResult } from "../reconciliation/Prepare";
import { SettlementResult } from "../settlements/Prepare";
import type { Source } from "./Evidence";
import type { Plan } from "./Planning";
import { Posting, ProductionPosting } from "./Posting";
import { ReviewDecision } from "./ReviewDecision";
import { VendorCredit } from "./VendorCredit";
import { workspaceRequest } from "./workspace";

export type Close = { id: string; entity: string; period: string } & Plan;
type Saved<K, P> = { kind: K; paper: P & { sources?: Source[]; revises?: string; blockers?: string[] } };
export type Paper = { id: string; created_at: string; review_state: string } & (
  | Saved<"prepaid", PrepaidWorkpaper>
  | Saved<"accrual", AccrualWorkpaper>
  | Saved<"reconciliation", ReconciliationWorkpaper>
  | Saved<"settlement", SettlementWorkpaper>
);

export const paperLabels = { prepaid: "Prepaid schedule", accrual: "Accrued expense", reconciliation: "Statement reconciliation", settlement: "Stripe settlement" };
export const paperPaths = { prepaid: "prepaids", accrual: "accruals", reconciliation: "reconciliations", settlement: "settlements" };
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

function citedLines(location: string) {
  const match = /lines?\s+(\d+)(?:\s*(?:-|–|to)\s*(\d+))?/i.exec(location);
  if (!match) return undefined;
  const first = Number(match[1]);
  return [first, Number(match[2] ?? first)] as const;
}

function EvidencePane({ closeId, sources }: { closeId: string; sources: Source[] }) {
  const [active, setActive] = useState(0);
  const [texts, setTexts] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
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
  const cited = citedLines(source.location);
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
                <span>{number}</span>
                <span>{line || " "}</span>
              </div>
            );
          })}
        </div>
      )}
      <div className="evidence-meta">
        <strong>{source.name}</strong>
        <span className="chip">Cited: {source.location}</span>
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

export function WorkpaperResult({ close, saved }: { close: Close; saved: Paper }) {
  if (saved.kind === "settlement") return <SettlementResult result={saved.paper} />;
  if (saved.kind === "accrual") return <AccrualResult result={saved.paper} />;
  if (saved.kind === "reconciliation") return <ReconciliationResult result={saved.paper} />;
  const paper = saved.paper;
  return (
    <>
      <p>Source: {paper.input.source_reference} · user-supplied, unverified.</p>
      {paper.carry_forward ? (
        <section>
          <h3>Carried schedule · fresh review required</h3>
          <p>
            From <Link to={`/dashboard/closes/${paper.carry_forward.close_id}`}>{paper.carry_forward.period} · draft #{paper.carry_forward.paper_id}</Link>. Opening scheduled prepaid balance: ${paper.carry_forward.opening_balance} USD.
          </p>
          <p>
            Schedule only: this is not a verified ledger opening balance. No journal is proposed. Review current contracts, credits, account mappings and prior postings before accounting action. To correct the schedule, revise and review its predecessor, then carry that new version forward.
          </p>
        </section>
      ) : (
        <p>
          Method: {paper.input.method}, final-month rounding. {paper.input.booked_balance ? `Booked prepaid $${paper.input.booked_balance} · ${paper.input.booking_reference}.` : "Fully expensed with no prior adjustments."} {paper.input.credit_amount ? `Credit $${paper.input.credit_amount} effective ${paper.input.credit_period} · ${paper.input.credit_reference}.` : ""} {paper.input.cancellation_method ? `Cancellation ${paper.input.cancellation_method} effective ${paper.input.cancellation_period} · ${paper.input.cancellation_reference}.` : ""}
        </p>
      )}
      <p>
        Period expense: ${paper.period_expense}; recognized: ${paper.recognized_expense}; remaining: ${paper.prepaid_balance} USD.
      </p>
      <p>
        {paper.proposal
          ? `Draft adjustment ${paper.proposal.date}: debit ${paper.proposal.debit_account}, credit ${paper.proposal.credit_account}, $${paper.proposal.amount}.`
          : "No reclassification proposed."}
      </p>
      <table>
        <caption>Saved prepaid schedule</caption>
        <thead>
          <tr>
            <th>Month</th>
            <th>Expense USD</th>
            <th>Remaining USD</th>
            <th>Credit USD</th>
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
      {!paper.carry_forward && paper.input.method === "equal_full_months_v1" && !paper.input.credit_amount && !paper.input.booked_balance && !paper.input.cancellation_method && (
        <>
          <Posting closeId={close.id} paperId={saved.id} />
          <ProductionPosting closeId={close.id} paperId={saved.id} />
        </>
      )}
      <VendorCredit closeId={close.id} paperId={saved.id} paper={paper} sources={paper.sources ?? []} approved={saved.review_state === "approved"} />
    </>
  );
}

export default function WorkpaperView() {
  const { id, paperId } = useParams();
  const { close, papers, more, error, loading, reload } = useClose(id);
  const [older, setOlder] = useState<Paper>();
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
              {paperLabels[saved.kind]} · draft #{saved.id}
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
            <Link className="button" to={`/dashboard/${paperPaths[saved.kind]}/new?close=${close.id}&revise=${saved.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`}>
              Revise and recalculate
            </Link>
          )}
        </div>
      </div>
      {more && <p className="note">Review navigation covers the latest 100 drafts in this close.</p>}
      <div className="canvas">
        <EvidencePane key={saved.id} closeId={close.id} sources={saved.paper.sources ?? []} />
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
          <details>
            <summary>Original inputs and full calculation</summary>
            <pre>{JSON.stringify(saved.paper, null, 2)}</pre>
          </details>
        </div>
      </div>
    </>
  );
}
