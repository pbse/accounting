import { type ReactNode, useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "../closes/workspace";

export type CardAction = "prepare" | "recheck" | "stop";
export type Outcome = { at: string; run_id?: string; jobs: { id: string; name: string }[] };
type Job = { id: string; name: string; state: string };
type Status = {
  inventory: { job_id: string; source_id: string; name: string; state: string }[];
  inventory_digest: string;
  coverage: null | { stale: boolean };
  stop: { digest: string; jobs: Job[] };
  recheck: { digest: string; jobs: Job[] };
  run: null | { id: string; inventory_digest: string; actor: string; created_at: string; items: { state: string }[] };
};
type Work = { id: string; state: string; error: string; stale: boolean; workpaper_ids: string[] };

const titles: Record<CardAction, string> = { prepare: "Prepare this close", recheck: "Recheck failed or pending proposals", stop: "Stop unresolved work" };
const done: Record<CardAction, string> = { prepare: "Preparing", recheck: "Rechecking", stop: "Stopped" };
const agree: Record<CardAction, string> = {
  prepare: "Coordinate these source documents; facts and accounting methods still need approval.",
  recheck: "Recheck the listed proposals.",
  stop: "I reviewed this close and intend to stop the listed jobs.",
};
const nothing: Record<CardAction, string> = { prepare: "Upload evidence to this close first.", recheck: "Nothing is waiting to be rechecked.", stop: "No unresolved work to stop." };
const words = (state: string) => state.replaceAll("_", " ");
const jobs = (n: number) => `${n} job${n === 1 ? "" : "s"}`;

// A close action proposed in the assistant. It lists exactly what the close page's route would act on, runs only after
// its author confirms that list, and then follows the jobs its confirmation covered.
export function ActionCard({ closeId, action, outcome, live, onConfirm }: { closeId: string; action: CardAction; outcome?: Outcome | null; live: boolean; onConfirm: (body: { digest: string; reason: string }) => Promise<void> }) {
  const [status, setStatus] = useState<Status>();
  const [work, setWork] = useState<Work[]>();
  const [error, setError] = useState("");
  const [failure, setFailure] = useState("");
  const [confirmed, setConfirmed] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const settled = !!outcome;
  useEffect(() => {
    if (!live) return;
    let on = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = () =>
      (settled ? workspaceRequest(`/${closeId}/work?view=all&jobs=${outcome?.jobs.slice(0, 100).map((j) => j.id).join(",") || "0"}`).then((d) => on && setWork(d.items)) : workspaceRequest(`/${closeId}/prepare-close`).then((d: Status) => on && setStatus(d)))
        .then(() => on && setError(""))
        .catch((e) => on && setError(e instanceof Error ? e.message : "Close preparation unavailable."))
        .finally(() => on && (timer = setTimeout(poll, 5000)));
    void poll();
    return () => {
      on = false;
      clearTimeout(timer);
    };
  }, [closeId, live, settled]);
  const scope = !status ? undefined : action === "prepare" ? { digest: status.inventory_digest, jobs: status.inventory.map((i) => ({ id: i.job_id, name: `${i.name} · source #${i.source_id}`, state: i.state })) } : { ...status[action], jobs: status[action].jobs.map((j) => ({ ...j, name: `${j.name} · job #${j.id}` })) };
  // Any change to the list clears the tick, even if the list later returns to what was ticked.
  useEffect(() => setConfirmed(""), [scope?.digest]);
  const card = (body: ReactNode) => (
    <div className="assistant-card">
      <strong>{titles[action]}</strong>
      {body}
      {error && <p role="alert">{error}</p>}
      {failure && <p role="alert">{failure}</p>}
    </div>
  );
  if (outcome) {
    const current = new Map((work ?? []).map((w) => [w.id, w]));
    return card(
      <>
        <p>
          {done[action]} {jobs(outcome.jobs.length)}
          {outcome.run_id ? ` as run #${outcome.run_id}` : ""} · confirmed {new Date(outcome.at).toLocaleString()}
        </p>
        {live && work && (
          <ul>
            {outcome.jobs.slice(0, 100).map((j) => {
              const w = current.get(j.id);
              return (
                <li key={j.id}>
                  {j.name} · job #{j.id}: {w ? `${words(w.state)}${w.stale ? " · source changed" : ""}${w.error ? ` · ${w.error}` : ""}` : "no longer readable; open Work"}
                  {w?.workpaper_ids.map((id) => (
                    <Link key={id} to={`/dashboard/closes/${closeId}/workpapers/${id}`}>
                      Draft #{id} →
                    </Link>
                  ))}
                </li>
              );
            })}
            {outcome.jobs.length > 100 && <li>and {outcome.jobs.length - 100} more; open Work for their state.</li>}
          </ul>
        )}
      </>,
    );
  }
  if (!live) return card(<p>A newer card replaced this one.</p>);
  if (!status || !scope) return card(!error && <p role="status">Loading the current work…</p>);
  const run = status.run;
  const running = action === "prepare" && run?.inventory_digest === status.inventory_digest;
  async function submit() {
    setBusy(true);
    setFailure("");
    try {
      await onConfirm({ digest: confirmed, reason: reason.trim() });
    } catch (e) {
      // Kept until the next confirmation, so a result the card could not record stays visible.
      setFailure(e instanceof Error ? e.message : "The action could not be confirmed.");
      setConfirmed("");
    } finally {
      setBusy(false);
    }
  }
  return card(
    <>
      {scope.jobs.length === 0 ? (
        <p>
          {nothing[action]} {action === "prepare" && <Link to={`/dashboard/closes/${closeId}?tab=evidence`}>Open evidence →</Link>}
        </p>
      ) : running ? (
        <p>This exact scope is already being prepared as run #{run.id}.</p>
      ) : (
        <>
          <p>{action === "prepare" ? `${scope.jobs.length} source document${scope.jobs.length === 1 ? "" : "s"}:` : `${jobs(scope.jobs.length)}:`}</p>
          <ul>
            {scope.jobs.map((j) => (
              <li key={j.id}>
                {j.name} · {words(j.state)}
              </li>
            ))}
          </ul>
          {action === "prepare" && !status.coverage && <p>No QuickBooks account coverage is linked, so this covers the uploaded documents only.</p>}
          {action === "prepare" && status.coverage?.stale && <p role="alert">The ledger capture is stale. Recapture it before relying on account suggestions.</p>}
          {action === "stop" && (
            <label>
              Reason for stopping
              <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
            </label>
          )}
          <label>
            <input type="checkbox" checked={confirmed === scope.digest} onChange={(e) => setConfirmed(e.target.checked ? scope.digest : "")} /> {agree[action]}
          </label>
          <p className="assistant-note">Confirming needs the workpapers:review permission and runs the close page's own checks.</p>
          <button type="button" className="primary" disabled={busy || confirmed !== scope.digest || (action === "stop" && !reason.trim())} onClick={() => void submit()}>
            {busy ? "Confirming…" : titles[action]}
          </button>
        </>
      )}
      {run && (
        <p role="status">
          Close preparation run #{run.id} · started by {run.actor} · {new Date(run.created_at).toLocaleString()} ·{" "}
          {Object.entries(run.items.reduce<Record<string, number>>((n, i) => ({ ...n, [words(i.state)]: (n[words(i.state)] ?? 0) + 1 }), {}))
            .map(([state, count]) => `${count} ${state}`)
            .join(" · ")}
        </p>
      )}
    </>,
  );
}
