import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";
import { Coverage } from "./Coverage";

type Item = {
  job_id: string;
  source_id: string;
  name: string;
  state: string;
  error: string;
  stale: boolean;
  workpaper_ids: string[];
  proposal?: {
    kind?: string;
    questions?: string[];
  };
  reviews: { id: string; state: string; blockers?: string[] }[];
};
type Status = {
  coverage: null | { snapshot_id: string; source_id: string; period: string; captured_at: string; stale: boolean; items: { account_id: string; name: string; kind: string; reason: string; question: string; location: string; report_locations?: string[] }[] };

  inventory: { job_id: string; source_id: string; name: string; state: string; kind: string; summary: string; questions: string[] }[];
  inventory_digest: string;
  stop: { count: number; digest: string };
  run: null | {
    id: string;
    inventory_digest: string;
    actor: string;
    created_at: string;
    items: Item[];
  };
};
export function Coordinator({ closeId }: { closeId: string }) {
  function revealWork(jobID: string) {
    const target = document.getElementById(`work-${jobID}`);
    if (target instanceof HTMLDetailsElement) target.open = true;
  }
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [stopConfirmed, setStopConfirmed] = useState(false);
  const [stopReason, setStopReason] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    async function poll() {
      try {
        const next: Status = await workspaceRequest(
          `/${closeId}/prepare-close`,
        );
        if (live) {
          setStatus(next);
          setError("");
        }
      } catch (e) {
        if (live)
          setError(
            e instanceof Error ? e.message : "Close preparation unavailable.",
          );
      } finally {
        if (live) timer = setTimeout(() => void poll(), 5000);
      }
    }
    void poll();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [closeId, version]);
  useEffect(() => setConfirmed(false), [status?.inventory_digest]);
  useEffect(() => setStopConfirmed(false), [status?.stop.digest]);
  useEffect(() => setStopReason(""), [closeId]);
  async function start() {
    if (!status) return;
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/prepare-close`, {
        inventory_digest: status.inventory_digest,
        confirmed,
      });
      setConfirmed(false);
      setVersion((v) => v + 1);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "Preparation could not be confirmed.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function stopRemaining() {
    if (!status) return;
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/prepare-close/cancel`, {
        digest: status.stop.digest,
        reason: stopReason.trim(),
        confirmed: stopConfirmed,
      });
      setStopReason("");
      setStopConfirmed(false);
      setVersion((v) => v + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not stop work.");
    } finally {
      setBusy(false);
    }
  }
  const run = status?.run;
  const changed = run && run.inventory_digest !== status?.inventory_digest;
  const items = run?.items ?? [];
  const stopped = items.filter((i) => i.state === "cancelled").length;
  const blocked = items.filter(
    (i) =>
      i.state !== "cancelled" && (
        i.stale ||
        ["failed", "missing"].includes(i.state) ||
        i.proposal?.kind === "unsupported" ||
        i.reviews.some(
          (r) =>
            ["source_changed", "changes_requested"].includes(r.state) ||
            (r.blockers?.length ?? 0) > 0,
        )
      ),
  );
  const complete =
    items.length > 0 &&
    items.every((i) => i.state === "completed") &&
    blocked.length === 0 && stopped === 0;
  const proposed = items.reduce<Record<string, number>>((counts, item) => {
    const kind = item.proposal?.kind || "awaiting interpretation";
    counts[kind] = (counts[kind] ?? 0) + 1;
    return counts;
  }, {});
  const questions = new Map<string, { text: string; jobs: Item[] }>();
  for (const item of items) {
    if (!["needs_confirmation", "waiting_client"].includes(item.state))
      continue;
    for (const text of item.proposal?.questions ?? []) {
      const key = text.trim().toLowerCase();
      if (!key) continue;
      const q = questions.get(key) ?? { text, jobs: [] };
      if (!q.jobs.some((j) => j.job_id === item.job_id)) q.jobs.push(item);
      questions.set(key, q);
    }
  }
  const decisions = items.map((item) => {
    if (item.stale || ["failed", "missing"].includes(item.state))
      return { item, action: "Inspect changed evidence or retry preparation" };
    if (item.state === "needs_confirmation")
      return { item, action: "Confirm proposed facts and method" };
    if (item.state === "waiting_client")
      return { item, action: "Track the client request and review the reply" };
    if (item.state === "completed" && item.reviews.some((review) =>
      review.state !== "approved" || (review.blockers?.length ?? 0) > 0,
    )) return { item, action: "Review the prepared draft and its blockers" };
    return null;
  }).filter((value): value is { item: Item; action: string } => value !== null);
  return (
    <section className="panel" aria-labelledby="prepare-close-title">
      <h2 id="prepare-close-title">Prepare this close</h2>
      <p>
        Coordinate uploaded evidence through interpretation, questions,
        confirmed facts and prepared drafts. Review and sign-off remain
        separate.
      </p>
      {error && <p role="alert">{error}</p>}
      {!status && !error && <p role="status">Loading preparation scope…</p>}
      {status && (
        <>
          {(!run || changed) && (
            <>
              {changed && (
                <p role="status">
                  Evidence scope changed. Review the current list before
                  starting an updated run; existing jobs and answers are reused.
                </p>
              )}
              {status.inventory.length === 0 ? (
                <p>
                  Upload evidence above to begin. Existing manual and carried
                  drafts remain in the review section.
                </p>
              ) : (
                <>
                  <details>
                    <summary>
                      Proposed scope · {status.inventory.length} source document
                      {status.inventory.length === 1 ? "" : "s"} in scope
                    </summary>
                    <p>Based on the uploaded documents and the ledger capture below. An account or document absent from this inventory is not covered.</p>
                    <ul>
                      {status.inventory.map((i) => (
                        <li key={i.job_id}>
                          {i.name} · source #{i.source_id} · {i.kind ? `${i.kind.replaceAll("_", " ")} proposed` : "awaiting interpretation"} · {i.state.replaceAll("_", " ")}
                          {i.summary && <span> — {i.summary}</span>}
                          {!!i.questions?.length && <span> · {i.questions.length} question{i.questions.length === 1 ? "" : "s"}</span>}
                        </li>
                      ))}
                    </ul>
                  </details>
                  {!status.coverage && <p role="status">No QuickBooks or Xero account coverage is linked. This proposal covers uploaded documents only; capture the month from QuickBooks or Xero, or document other ledger work, before signing off.</p>}
                  {status.coverage?.stale && <p role="alert">The ledger capture is stale. Recapture it before relying on these account suggestions.</p>}
                  {status.coverage && <details>
                    <summary>{status.coverage.items.length} ledger accounts in proposed scope</summary>
                    <p>Account type suggests work; it does not establish the complete invoice set or approve a method. Review account evidence and explicit exclusions below.</p>
                    <ul>{status.coverage.items.slice(0, 20).map((account) => <li key={account.account_id}>{account.name || account.account_id} · {account.kind.replaceAll("_", " ")} · {account.question}</li>)}</ul>
                    {status.coverage.items.length > 20 && <p>Showing the first 20 here. <a href="#coverage-work">Search and review all {status.coverage.items.length} accounts in Coverage.</a></p>}
                  </details>}
                  <label>
                    <input
                      type="checkbox"
                      checked={confirmed}
                      onChange={(e) => setConfirmed(e.target.checked)}
                    />{" "}
                    Coordinate these source documents; facts and accounting
                    methods still need approval.
                  </label>
                  <button
                    type="button"
                    disabled={busy || !confirmed || !!error}
                    onClick={() => void start()}
                  >
                    {busy ? "Recording scope…" : "Prepare this close"}
                  </button>
                </>
              )}
            </>
          )}
          {run && (
            <>
              <h3>Next decisions</h3>
              <p><a href="#coverage-work">{status.coverage === null
                ? "Capture period reports or document ledger scope separately"
                : status.coverage.stale
                  ? "Recapture changed ledger coverage"
                  : "Review ledger account coverage and missing evidence"}</a></p>
              {decisions.length === 0
                ? <p>No source decisions are waiting. Review coverage and prepared drafts before sign-off.</p>
                : <ul>{decisions.slice(0, 10).map(({ item, action }) =>
                  <li key={item.job_id}><a href={`#work-${item.job_id}`} onClick={() => revealWork(item.job_id)}>{action}: {item.name}</a></li>,
                )}</ul>}
              {decisions.length > 10 &&
                <p>{decisions.length - 10} more decisions are listed in Work below.</p>}
              <h3>Proposed work from this run</h3>
              <p>
                {Object.entries(proposed).map(([kind, count]) => `${count} ${kind.replaceAll("_", " ")}`).join(" · ")}. {status.coverage === null
                  ? "No ledger coverage report is linked."
                  : status.coverage.stale
                    ? "Ledger coverage has changed and needs recapture."
                    : `${status.coverage.items.length} ledger account coverage proposals need review.`}
              </p>
              <p>These proposals cover the uploaded sources below. Check the client’s full account and document inventory before defining the final close scope.</p>
              <p role="status">
                Run #{run.id} ·{" "}
                {changed
                  ? "scope changed"
                  : blocked.length || stopped
                    ? `${blocked.length} blocked · ${stopped} stopped`
                    : complete
                      ? "drafts prepared — review required"
                      : `${items.filter((i) => i.state === "completed").length} of ${items.length} items prepared`}
              </p>
              <p>
                Started by {run.actor} ·{" "}
                {new Date(run.created_at).toLocaleString()}
              </p>
              <ul>
                {items.map((i) => (
                  <li key={i.job_id}>
                    <a href={`#work-${i.job_id}`} onClick={() => revealWork(i.job_id)}>{i.name}</a> ·{" "}
                    {i.proposal?.kind ? `${i.proposal.kind.replaceAll("_", " ")} · ` : ""}{i.state.replaceAll("_", " ")}
                    {i.stale ? " · source changed" : ""}
                    {i.error ? ` · ${i.error}` : ""}
                    {i.proposal?.kind === "unsupported"
                      ? " · unsupported accounting work"
                      : ""}
                    {i.reviews.map((r) => (
                      <span key={r.id}>
                        {" "}
                        · draft #{r.id}: {r.state.replaceAll("_", " ")}
                        {r.blockers?.length
                          ? ` (${r.blockers.join("; ")})`
                          : ""}
                      </span>
                    ))}
                  </li>
                ))}
              </ul>
              {questions.size > 0 && (
                <>
                  <h3>Questions to resolve</h3>
                  <p>
                    Each answer stays linked to its original job. Repeated
                    wording is grouped here; confirm whether the same answer
                    applies to each source.
                  </p>
                  <ul>
                    {[...questions.entries()].map(([key, q]) => (
                      <li key={key}>
                        {q.text} —{" "}
                        {q.jobs.map((j, index) => (
                          <span key={j.job_id}>
                            {index > 0 ? ", " : ""}
                            <a href={`#work-${j.job_id}`} onClick={() => revealWork(j.job_id)}>{j.name}</a>
                          </span>
                        ))}
                      </li>
                    ))}
                  </ul>
                </>
              )}
              <p>
                <a href="#close-work">
                  Review facts, answer questions and inspect client requests
                </a>
                . Approved replies resume the same durable jobs automatically.
              </p>
            </>
          )}
          {status.stop.count > 0 && (
            <details>
              <summary>Stop {status.stop.count} unresolved evidence job{status.stop.count === 1 ? "" : "s"} in this close</summary>
              <p>Pending preparation will stop and unused client reply links will be revoked. Prepared drafts and messages already sent remain in their history. Each stopped job can be restarted from Work.</p>
              <label>Reason for stopping<textarea value={stopReason} maxLength={500} onChange={(e) => setStopReason(e.target.value)} /></label>
              <label><input type="checkbox" checked={stopConfirmed} onChange={(e) => setStopConfirmed(e.target.checked)} /> I reviewed this close and intend to stop all unresolved evidence jobs.</label>
              <button type="button" disabled={busy || !stopConfirmed || !stopReason.trim()} onClick={() => void stopRemaining()}>{busy ? "Stopping…" : "Stop remaining work"}</button>
            </details>
          )}
          <Coverage closeId={closeId} />
        </>
      )}
    </section>
  );
}
