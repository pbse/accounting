import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { Link } from "react-router";
import { workspaceKey, workspaceQuery } from "../../data/queries";
import { ProposedRequest } from "../closes/ProposedRequest";

type Outcome = { at?: string; draft_id?: string; questions?: number; kept?: string[] };

// A request card drafts the close's client request, the same one its goal card and Requests show, and then shows it here
// to reword, add to, approve and send. Drafting sends nothing.
export function RequestCard({ closeId, outcome, live, onConfirm }: { closeId: string; outcome?: Outcome | null; live: boolean; onConfirm: (body: Record<string, never>) => Promise<void> }) {
  const read = useQuery({ ...workspaceQuery<{ proposal: { id: string } | null }>(`/${closeId}/requests/proposed`), enabled: live });
  const queries = useQueryClient();
  const [failure, setFailure] = useState("");
  const [busy, setBusy] = useState(false);
  const [approved, setApproved] = useState(false);
  const requests = `/dashboard/closes/${closeId}?tab=requests`;
  const card = (body: ReactNode) => (
    <div className="assistant-card">
      <strong>Client request</strong>
      {body}
      {failure && <p role="alert">{failure}</p>}
    </div>
  );
  if (!live)
    return card(<p>{outcome?.draft_id ? `Drafted request #${outcome.draft_id}. A newer card replaced this one.` : "A newer card replaced this one."}</p>);
  async function draft() {
    setBusy(true);
    setFailure("");
    try {
      await onConfirm({});
      await queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests/proposed`) });
    } catch (e) {
      setFailure(e instanceof Error ? e.message : "The request could not be drafted.");
    } finally {
      setBusy(false);
    }
  }
  const none = read.data && !read.data.proposal && !approved;
  return card(
    <>
      {read.isError && <p role="alert">The drafted request could not be read. {read.error.message}</p>}
      {read.isPending && <p role="status">Reading the close's drafted request…</p>}
      {/* Kept mounted, so an approval made here keeps showing its link and passcode. */}
      <ProposedRequest closeId={closeId} onApproved={() => setApproved(true)} />
      {none &&
        (outcome ? (
          <>
            <p>The client has nothing to answer for this month now.</p>
            {(outcome.kept?.length ?? 0) > 0 && (
              <details>
                <summary>Kept with the firm, not asked of the client · {outcome.kept!.length}</summary>
                <ul>
                  {outcome.kept!.map((k) => (
                    <li key={k}>{k}</li>
                  ))}
                </ul>
              </details>
            )}
            <p>
              <Link to={`${requests}&view=draft`}>Ask a question of your own in Requests →</Link>
            </p>
          </>
        ) : (
          <>
            <p>The agent drafts one request for everything this month needs from the client, each question saying what it unblocks. Nothing is sent.</p>
            <button type="button" className="primary" disabled={busy} onClick={() => void draft()}>
              {busy ? "Drafting…" : "Draft the request"}
            </button>
          </>
        ))}
    </>,
  );
}
