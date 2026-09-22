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

  inventory: { job_id: string; source_id: string; name: string }[];
  inventory_digest: string;
  run: null | {
    id: string;
    inventory_digest: string;
    actor: string;
    created_at: string;
    items: Item[];
  };
};
export function Coordinator({ closeId }: { closeId: string }) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
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
  const run = status?.run;
  const changed = run && run.inventory_digest !== status?.inventory_digest;
  const items = run?.items ?? [];
  const blocked = items.filter(
    (i) =>
      i.stale ||
      ["failed", "cancelled", "missing"].includes(i.state) ||
      i.proposal?.kind === "unsupported" ||
      i.reviews.some(
        (r) =>
          ["source_changed", "changes_requested"].includes(r.state) ||
          (r.blockers?.length ?? 0) > 0,
      ),
  );
  const complete =
    items.length > 0 &&
    items.every((i) => i.state === "completed") &&
    blocked.length === 0;
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
          <Coverage closeId={closeId} />
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
                  Upload evidence below to begin. Existing manual and carried
                  drafts remain in the review section.
                </p>
              ) : (
                <>
                  <details>
                    <summary>
                      {status.inventory.length} source document
                      {status.inventory.length === 1 ? "" : "s"} in scope
                    </summary>
                    <ul>
                      {status.inventory.map((i) => (
                        <li key={i.job_id}>
                          {i.name} · source #{i.source_id}
                        </li>
                      ))}
                    </ul>
                  </details>
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
              <p role="status">
                Run #{run.id} ·{" "}
                {changed
                  ? "scope changed"
                  : blocked.length
                    ? `${blocked.length} blocked items`
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
                    <a href={`#work-${i.job_id}`}>{i.name}</a> ·{" "}
                    {i.state.replaceAll("_", " ")}
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
                            <a href={`#work-${j.job_id}`}>{j.name}</a>
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
        </>
      )}
    </section>
  );
}
