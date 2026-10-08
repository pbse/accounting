import { useState } from "react";
import { Link } from "react-router";

type Outcome = { at: string } & Record<string, unknown>;

// ProposalCard is the agent's card for an action its own route has no page card for yet: asking a person to review a
// draft, or running a portfolio goal on the closes a preview listed. It shows exactly what was proposed and runs only
// when the author confirms; the server previews the goal again and refuses a changed selection.
export function ProposalCard({ kind, proposal, closeId = "", outcome, live, onConfirm }: { kind: "approval" | "portfolio"; proposal: Record<string, unknown>; closeId?: string; outcome?: Outcome | null; live: boolean; onConfirm: (body: { reason: string }) => Promise<void> }) {
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState("");
  const args = (proposal.args ?? {}) as Record<string, unknown>;
  const title = kind === "approval" ? "Ask a person to review a draft" : `Run a goal across closes`;
  const card = (body: React.ReactNode) => (
    <div className="assistant-card">
      <strong>{title}</strong>
      {body}
      {failure && <p role="alert">{failure}</p>}
    </div>
  );
  if (outcome) {
    if (kind === "portfolio")
      return card(
        <p>
          Started {String(outcome.action ?? "")} on {String(outcome.closes ?? "")} close{outcome.closes === 1 ? "" : "s"}
          {Number(outcome.pending ?? 0) > 0 ? `; ${String(outcome.pending)} still pending` : ""} · confirmed {new Date(outcome.at).toLocaleString()}. <Link to="/dashboard">Work →</Link>
        </p>,
      );
    return card(
      <p>
        Review requested for draft #{String(args.workpaper_id ?? "")} · confirmed {new Date(outcome.at).toLocaleString()}.{" "}
        <Link to={`/dashboard/closes/${closeId}/workpapers/${String(args.workpaper_id ?? "")}`}>Open the draft →</Link>
      </p>,
    );
  }
  if (!live) return card(<p>A newer card replaced this one.</p>);
  async function submit() {
    setBusy(true);
    setFailure("");
    try {
      await onConfirm({ reason: reason.trim() });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : "The action could not be confirmed.");
      setConfirmed(false);
    } finally {
      setBusy(false);
    }
  }
  const closes = Array.isArray(args.closes) ? (args.closes as string[]) : [];
  return card(
    <>
      {kind === "approval" ? (
        <p>
          Draft #{String(args.workpaper_id ?? "")}: “{String(args.note ?? "")}”
        </p>
      ) : (
        <>
          <p>“{String(args.text ?? "")}” on {closes.length} close{closes.length === 1 ? "" : "s"}, as previewed:</p>
          <ul>
            {closes.map((id) => (
              <li key={id}>
                <Link to={`/dashboard/closes/${id}`}>Close #{id}</Link>
              </li>
            ))}
          </ul>
          {/^\s*stop\b/i.test(String(args.text ?? "")) && (
            <label>
              Reason for stopping
              <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
            </label>
          )}
        </>
      )}
      <label>
        <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> {kind === "approval" ? "Ask for this review." : "Run this goal on exactly these closes; the server refuses a changed selection."}
      </label>
      <p className="assistant-note">Confirming needs the workpapers:review permission and runs the same checks as the page.</p>
      <button type="button" className="primary" disabled={busy || !confirmed} onClick={() => void submit()}>
        {busy ? "Confirming…" : title}
      </button>
    </>,
  );
}
