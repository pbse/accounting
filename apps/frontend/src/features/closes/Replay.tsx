import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Item = { job_id: string; source_name: string; confirmed_kind: string; replay_kind?: string; kept: boolean; changed_fields: string[]; error?: string };

export function Replay({ closeId }: { closeId: string }) {
  const [result, setResult] = useState<{ model: string; reasoning_effort?: string | null; items: Item[]; actor?: string; created_at?: string }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    workspaceRequest(`/${closeId}/replay`)
      .then((saved: { run: { model: string; reasoning_effort?: string | null; items: Item[]; actor: string; created_at: string } | null }) => saved.run && setResult((current) => current ?? saved.run!))
      .catch(() => undefined);
  }, [closeId]);
  async function run() {
    setBusy(true);
    setError("");
    try {
      setResult(await workspaceRequest(`/${closeId}/replay`, {}));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Replay failed. Nothing was saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Check the current agent">
      <div className="section-title">
        <h2>Check the current agent</h2>
        <button type="button" disabled={busy} onClick={() => void run()}>
          {busy ? "Replaying…" : "Replay confirmed readings"}
        </button>
      </div>
      <p className="note">
        Reads up to 3 of this close's most recently confirmed documents again with the current model (up to 3 model calls), on the evidence the
        agent originally selected, and compares each reading with what was confirmed. Only the comparison is saved; no reading or workpaper changes.
        The worker runs the same check once for each new model and reasoning effort.
      </p>
      {error && <p role="alert">{error}</p>}
      {result?.created_at && (
        <p className="note">
          Saved {new Date(result.created_at).toLocaleString()} · {result.actor === "scheduled-replay" ? "checked automatically for this model and effort" : `run by ${result.actor}`}
        </p>
      )}
      {result && (
        <div className="rows">
          <div className="rows-head">
            <span>Document</span>
            <span>Agent ({result.model}; {result.reasoning_effort == null ? "reasoning effort not recorded" : `${result.reasoning_effort || "provider default"} reasoning effort`})</span>
            <span />
            <span />
          </div>
          {result.items.length === 0 && <p className="empty">No model readings have been confirmed in this close yet.</p>}
          {result.items.map((item) => (
            <div className="row" key={item.job_id}>
              <strong>{item.source_name}</strong>
              <span>
                {item.error ? (
                  <span className="status info">{item.error}</span>
                ) : item.kept ? (
                  <span className="status ok">Same as confirmed</span>
                ) : (
                  <span className="status warn">
                    Differs: {item.changed_fields.map((f) => f.replaceAll("_", " ")).join(", ")}
                    {item.replay_kind && item.replay_kind !== item.confirmed_kind ? ` (read as ${item.replay_kind}, confirmed as ${item.confirmed_kind})` : ""}
                  </span>
                )}
              </span>
              <span />
              <span />
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
