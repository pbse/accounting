import type { AccrualWorkpaper } from "../../../../../packages/contracts/accrual";
import { AccrualResult } from "../accruals/Prepare";
import { QuickBooks } from "./QuickBooks";
import { Coordinator } from "./Coordinator";
import { EmailSettings } from "./Email";
import { ClientAccess } from "./ClientAccess";
import { Procedures } from "./Procedures";
import { Readiness } from "./Readiness";
import { CarryForward } from "./CarryForward";
import { Recurring } from "./Recurring";
import { Work } from "./Work";
import { Requests } from "./Requests";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import {
  Evidence,
  PreparationJobs,
  SourceReview,
  type Source,
} from "./Evidence";
import { ReviewDecision } from "./ReviewDecision";
import { Posting } from "./Posting";
import { ProductionPosting } from "./Posting";
import { ProductionAccess } from "./ProductionAccess";
import { AccountAccess, authKitClientId } from "../auth/Auth";
import { unlock, workspaceRequest } from "./workspace";
import type { PrepaidWorkpaper } from "../../../../../packages/contracts/prepaid";
import type { ReconciliationWorkpaper } from "../../../../../packages/contracts/reconciliation";
import { ReconciliationResult } from "../reconciliation/Prepare";

type Close = { id: string; entity: string; period: string };
type Paper = { id: string; created_at: string; review_state: string } & (
  | {
      kind: "prepaid";
      paper: PrepaidWorkpaper & { sources?: Source[]; revises?: string };
    }
  | {
      kind: "accrual";
      paper: AccrualWorkpaper & { sources?: Source[]; revises?: string };
    }
  | {
      kind: "reconciliation";
      paper: ReconciliationWorkpaper & { sources?: Source[]; revises?: string };
    }
);
export default function Closes() {
  const { id } = useParams();
  const navigate = useNavigate();
  const generation = useRef(0);
  const [items, setItems] = useState<Close[]>([]);
  const [close, setClose] = useState<Close>();
  const [papers, setPapers] = useState<Paper[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [more, setMore] = useState(false);
  const [reviewFilter, setReviewFilter] = useState("all");
  const [exporting, setExporting] = useState(false);
  const [exportStatus, setExportStatus] = useState("");
  async function load() {
    const current = ++generation.current;
    setBusy(true);
    setError("");
    setLoaded(false);
    setClose(undefined);
    setPapers([]);
    setItems([]);
    try {
      const data = await workspaceRequest(
        id ? `/${encodeURIComponent(id)}` : "",
      );
      if (current !== generation.current) return;
      setClose(data.close);
      setItems(data.closes ?? []);
      setPapers(data.workpapers ?? []);
      setMore(data.has_more);
      setLoaded(true);
    } catch (e) {
      if (current !== generation.current) return;
      setError(
        e instanceof Error ? e.message : "Could not open the workspace.",
      );
    } finally {
      if (current === generation.current) setBusy(false);
    }
  }
  useEffect(() => {
    void load();
    return () => {
      generation.current++;
    };
  }, [id]);
  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    setBusy(true);
    setError("");
    try {
      const saved = await workspaceRequest("", {
        entity: data.get("entity"),
        period: data.get("period"),
      });
      navigate(`/dashboard/closes/${saved.id}`);
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Save failed. Retry the same client and month safely.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p className="eyebrow">PRIVATE WORKSPACE / DRAFTS ONLY</p>
      <h1>{close ? `${close.entity} · ${close.period}` : "Client closes"}</h1>
      <p>
        Keep prepared work together by client and month. Saving does not approve
        or post it. Sandbox execution and supported-scope sign-off are tracked
        separately below.
      </p>
      {authKitClientId ? (
        <AccountAccess />
      ) : (
        <>
          <details open={!loaded}>
            <summary>Workspace access</summary>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                unlock(
                  String(new FormData(event.currentTarget).get("token") ?? ""),
                );
                event.currentTarget.reset();
                void load();
              }}
            >
              <label>
                Private workspace token
                <input
                  name="token"
                  type="password"
                  autoComplete="off"
                  required
                  minLength={32}
                />
              </label>
              <button disabled={busy}>Unlock workspace</button>
            </form>
            <p>
              The server-configured token stays in this tab’s memory. Refreshing
              locks access again. Hosted user login will replace this
              development access method.
            </p>
          </details>
          <button
            disabled={busy}
            onClick={() => {
              unlock("");
              setLoaded(false);
              setClose(undefined);
              setPapers([]);
              setItems([]);
              setError("");
            }}
          >
            Lock workspace
          </button>
        </>
      )}
      {authKitClientId && (
        <button disabled={busy} onClick={() => void load()}>
          Reload workspace
        </button>
      )}
      {error && <p role="alert">{error}</p>}
      {busy && <p role="status">Opening workspace…</p>}
      {loaded && !id && (
        <>
          <EmailSettings />
          <ClientAccess entities={[...new Set(items.map((c) => c.entity))]} />
          <section>
            <h2>Create a client close</h2>
            <form onSubmit={create}>
              <fieldset disabled={busy}>
                <div className="form-grid">
                  <label>
                    Client / entity
                    <input name="entity" required maxLength={200} />
                  </label>
                  <label>
                    Close month
                    <input name="period" type="month" required />
                  </label>
                </div>
                <button className="primary">Create or open close</button>
              </fieldset>
            </form>
          </section>
          <section>
            <h2>Saved closes</h2>
            {items.length === 0 ? (
              <p>No closes yet. Create your first client close above.</p>
            ) : (
              <ul>
                {items.map((item) => (
                  <li key={item.id}>
                    <Link to={`/dashboard/closes/${item.id}`}>
                      {item.entity} · {item.period}
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </section>
        </>
      )}
      {loaded && close && (
        <>
          <p>
            <Link to="/dashboard/closes">All client closes</Link>
          </p>
          <Evidence
            key={`evidence-${close.id}`}
            closeId={close.id}
            onReplace={() => void load()}
          />
          <QuickBooks
            key={`qbo-${close.id}`}
            closeId={close.id}
            entity={close.entity}
          />
          <ProductionAccess closeId={close.id} entity={close.entity} />
          <Recurring key={`recurring-${close.id}`} closeId={close.id} onPrepared={() => void load()} />
          <Coordinator key={`coordinator-${close.id}`} closeId={close.id} />
          <Work
            key={`work-${close.id}`}
            closeId={close.id}
            onPrepared={() => void load()}
          />
          <PreparationJobs
            key={`jobs-${close.id}`}
            closeId={close.id}
            onComplete={() => void load()}
          />
          <Readiness
            key={`readiness-${close.id}`}
            closeId={close.id}
            papers={papers}
          />
          <Procedures
            key={`procedures-${close.id}`}
            closeId={close.id}
            entity={close.entity}
            onApplied={() => void load()}
          />
          <CarryForward key={close.id} closeId={close.id} />
          <div className="export-actions">
            <Link
              className="button primary"
              to={`/dashboard/prepaids/new?close=${close.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`}
            >
              Prepare a prepaid
            </Link>
            <Link className="button" to={`/dashboard/accruals/new?close=${close.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`}>Prepare an accrued expense</Link>
            <Link
              className="button"
              to={`/dashboard/reconciliations/new?close=${close.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`}
            >
              Prepare statement reconciliation
            </Link>
          </div>
          <section>
            <h2>Take the close into review</h2>
            <p>
              Download every saved version, CSV tables, source references,
              unresolved items and stored sandbox receipts. The package records
              the supported scope and sign-off history; unlisted work is not
              certified.
            </p>
            <button
              disabled={exporting}
              onClick={async () => {
                setExporting(true);
                setExportStatus("");
                try {
                  const blob = await workspaceRequest(
                    `/${close.id}/export`,
                    undefined,
                    true,
                  );
                  const url = URL.createObjectURL(blob);
                  const link = document.createElement("a");
                  link.href = url;
                  link.download = `close-${close.id}-${close.period}.zip`;
                  link.click();
                  setTimeout(() => URL.revokeObjectURL(url), 1000);
                  setExportStatus(
                    "Package prepared. Open review.html inside the ZIP for the summary; your browser can print it to PDF.",
                  );
                } catch (e) {
                  setExportStatus(
                    e instanceof Error
                      ? e.message
                      : "Export failed. Your saved work has not changed.",
                  );
                } finally {
                  setExporting(false);
                }
              }}
            >
              {exporting
                ? "Preparing review package…"
                : "Download close review package"}
            </button>
            <p role="status">{exportStatus}</p>
          </section>
          <Requests closeId={close.id} papers={papers} />
          <h2>Saved workpapers</h2>
          {papers.length === 0 && (
            <p>
              No saved drafts for this close yet. Prepare a workpaper, then save
              it here.
            </p>
          )}
          <label>
            Review status
            <select
              value={reviewFilter}
              onChange={(e) => setReviewFilter(e.target.value)}
            >
              <option value="all">All drafts</option>
              <option value="pending">Awaiting review</option>
              <option value="approved">Approved workpapers</option>
              <option value="changes_requested">Changes requested</option>
              <option value="source_changed">Evidence changed</option>
            </select>
          </label>
          {papers
            .filter(
              (p) => reviewFilter === "all" || p.review_state === reviewFilter,
            )
            .map((saved) => (
              <details key={saved.id}>
                <summary>
                  {saved.kind === "prepaid"
                    ? "Prepaid schedule"
                    : saved.kind === "accrual" ? "Accrued expense" : "Statement reconciliation"}{" "}
                  · draft #{saved.id} ·{" "}
                  {new Date(saved.created_at).toLocaleString()} ·{" "}
                  {saved.review_state.replaceAll("_", " ")}
                </summary>
                {!(saved.kind === "prepaid" && saved.paper.carry_forward) && (
                  <Link
                    to={`/dashboard/${saved.kind === "prepaid" ? "prepaids" : saved.kind === "accrual" ? "accruals" : "reconciliations"}/new?close=${close.id}&revise=${saved.id}&entity=${encodeURIComponent(close.entity)}&period=${close.period}`}
                  >
                    Revise and recalculate
                  </Link>
                )}
                {saved.paper.revises && (
                  <p>
                    Recalculated from draft #{saved.paper.revises}. This
                    revision requires its own review.
                  </p>
                )}
                <ReviewDecision
                  closeId={close.id}
                  paperId={saved.id}
                  onChange={() => void load()}
                />
                {saved.paper.sources && (
                  <SourceReview
                    closeId={close.id}
                    sources={saved.paper.sources}
                  />
                )}
                {saved.kind === "accrual" ? <AccrualResult result={saved.paper} /> : saved.kind === "reconciliation" ? (
                  <ReconciliationResult result={saved.paper} />
                ) : (
                  <>
                    <p>
                      Source: {saved.paper.input.source_reference} ·
                      user-supplied, unverified.
                    </p>
                    {saved.paper.carry_forward ? (
                      <section>
                        <h3>Carried schedule · fresh review required</h3>
                        <p>
                          From{" "}
                          <Link
                            to={`/dashboard/closes/${saved.paper.carry_forward.close_id}`}
                          >
                            {saved.paper.carry_forward.period} · draft #
                            {saved.paper.carry_forward.paper_id}
                          </Link>
                          . Opening scheduled prepaid balance: $
                          {saved.paper.carry_forward.opening_balance} USD.
                        </p>
                        <p>
                          Schedule only: this is not a verified ledger opening
                          balance. No journal is proposed. Review current
                          contracts, credits, account mappings and prior
                          postings before accounting action. To correct the
                          schedule, revise and review its predecessor, then
                          carry that new version forward.
                        </p>
                      </section>
                    ) : (
                      <p>
                        Method: {saved.paper.input.method}, final-month rounding. {saved.paper.input.booked_balance ? `Booked prepaid $${saved.paper.input.booked_balance} · ${saved.paper.input.booking_reference}.` : "Fully expensed with no prior adjustments."} {saved.paper.input.credit_amount ? `Credit $${saved.paper.input.credit_amount} effective ${saved.paper.input.credit_period} · ${saved.paper.input.credit_reference}.` : ""} {saved.paper.input.cancellation_method ? `Cancellation ${saved.paper.input.cancellation_method} effective ${saved.paper.input.cancellation_period} · ${saved.paper.input.cancellation_reference}.` : ""}
                      </p>
                    )}
                    <p>
                      Period expense: ${saved.paper.period_expense}; recognized:
                      ${saved.paper.recognized_expense}; remaining: $
                      {saved.paper.prepaid_balance} USD.
                    </p>
                    <p>
                      {saved.paper.proposal
                        ? `Draft adjustment ${saved.paper.proposal.date}: debit ${saved.paper.proposal.debit_account}, credit ${saved.paper.proposal.credit_account}, $${saved.paper.proposal.amount}.`
                        : "No reclassification proposed."}
                    </p>
                    <table>
                      <caption>Saved prepaid schedule</caption>
                      <thead>
                        <tr>
                          <th>Month</th>
                          <th>Expense USD</th>
                          <th>Remaining USD</th><th>Credit USD</th>
                        </tr>
                      </thead>
                      <tbody>
                        {saved.paper.schedule.map((row) => (
                          <tr key={row.period}>
                            <th>{row.period}</th>
                            <td>{row.expense}</td>
                            <td>{row.remaining}</td><td>{row.credit ?? "—"}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </>
                )}
                {saved.kind === "prepaid" && !saved.paper.carry_forward && saved.paper.input.method==="equal_full_months_v1" && !saved.paper.input.credit_amount && !saved.paper.input.booked_balance && !saved.paper.input.cancellation_method && (
                  <>
                  <Posting closeId={close.id} paperId={saved.id} />
                  <ProductionPosting closeId={close.id} paperId={saved.id} />
                  </>
                )}
                <details>
                  <summary>Original inputs and full calculation</summary>
                  <pre>{JSON.stringify(saved.paper, null, 2)}</pre>
                </details>
              </details>
            ))}
        </>
      )}
      {more && (
        <p role="status">
          Showing the 100 most recent records. Older records remain saved in the
          database.
        </p>
      )}
    </>
  );
}
