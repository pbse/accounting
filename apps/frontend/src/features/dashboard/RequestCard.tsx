import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "../closes/workspace";

type Candidates = { workpapers: { id: string; kind: string; review_state: string }[]; more: number; body: string; digest: string };

// A request card saves the exact wording it shows as a draft in Requests; it never approves or sends a request.
export function RequestCard({ closeId, outcome, live, onConfirm }: { closeId: string; outcome?: { at?: string; draft_id?: string; created?: boolean } | null; live: boolean; onConfirm: (body: { digest: string }) => Promise<void> }) {
  const [shown, setShown] = useState<Candidates>();
  const [checked, setChecked] = useState("");
  const [error, setError] = useState("");
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  const requests = `/dashboard/closes/${closeId}?tab=requests`;
  useEffect(() => {
    if (outcome || !live) return;
    let on = true;
    workspaceRequest(`/${closeId}/requests/candidates`).then(
      (d: Candidates) => on && setShown(d),
      (e) => on && setError(e instanceof Error ? e.message : "Client requests are unavailable."),
    );
    return () => {
      on = false;
    };
  }, [closeId, outcome, live, version]);
  const card = (body: ReactNode) => (
    <div className="assistant-card">
      <strong>Consolidated client request</strong>
      {body}
      {error && <p role="alert">{error}</p>}
      {failure && <p role="alert">{failure}</p>}
    </div>
  );
  if (outcome)
    return card(
      <p>
        {outcome.created ? `Saved as request draft #${outcome.draft_id}.` : `Request draft #${outcome.draft_id} already has this wording.`} Nothing was approved or sent.{" "}
        <Link to={requests}>Edit and approve it in Requests →</Link>
      </p>,
    );
  if (!live) return card(<p>A newer card replaced this one.</p>);
  if (!shown) return card(!error && <p role="status">Reading the drafts a request would cover…</p>);
  if (shown.workpapers.length === 0)
    return card(
      <p>
        No draft needs information from the client now. <Link to={requests}>Write a specific request in Requests →</Link>
      </p>,
    );
  const current = shown;
  async function submit() {
    setBusy(true);
    setFailure("");
    try {
      await onConfirm({ digest: checked });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : "The request draft could not be saved.");
      setChecked("");
      setVersion((v) => v + 1);
    } finally {
      setBusy(false);
    }
  }
  return card(
    <>
      <p>
        It asks about {current.workpapers.length} draft{current.workpapers.length === 1 ? "" : "s"}:
      </p>
      <ul>
        {current.workpapers.map((p) => (
          <li key={p.id}>
            {p.kind} · draft #{p.id} · {p.review_state.replaceAll("_", " ")}
          </li>
        ))}
      </ul>
      {current.more > 0 && <p>And {current.more} more; select them in Requests.</p>}
      <p>The wording it would save:</p>
      <blockquote style={{ whiteSpace: "pre-line" }}>{current.body}</blockquote>
      <label>
        <input type="checkbox" checked={checked === current.digest} onChange={(e) => setChecked(e.target.checked ? current.digest : "")} /> Save this wording as a request draft. Nothing is approved or sent.
      </label>
      <button type="button" className="primary" disabled={busy || checked !== current.digest} onClick={() => void submit()}>
        {busy ? "Saving…" : "Save request draft"}
      </button>
    </>,
  );
}
