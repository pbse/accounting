import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { workspaceKey, workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { workspaceRequest } from "./workspace";
import { ProposedRequest } from "./ProposedRequest";

type Draft = { id: string; body: string; created_by: string; created_at: string; stale: boolean };

// The close's client request: the one the agent drafts from everything the month needs from the client, the same on the
// goal card, in chat and here, to reword, add to, approve and send. With none drafted, the agent drafts it now; when the
// client has nothing to answer, a person can still ask a question of their own.
export function Requests({ closeId }: { closeId: string }) {
  const dates = useDates();
  const proposed = useQuery(workspaceQuery<{ proposal: { id: string } | null }>(`/${closeId}/requests/proposed`));
  // Earlier wording stays inspectable: every saved draft, approved, replaced or withdrawn, newest first.
  const history = useQuery(workspaceQuery<Draft[]>(`/${closeId}/requests`));
  const queries = useQueryClient();
  const [kept, setKept] = useState<string[]>();
  const [question, setQuestion] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function draft(questions: string[]) {
    setBusy(true);
    setError("");
    try {
      const out: { kept: string[] } = await workspaceRequest(`/${closeId}/requests/proposed`, { questions });
      setKept(out.kept);
      setQuestion("");
      await queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests/proposed`) });
      void queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests`) });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The request could not be drafted.");
    } finally {
      setBusy(false);
    }
  }
  const none = !proposed.isError && proposed.data && !proposed.data.proposal;
  return (
    <section id="client-request">
      <h2>Client request</h2>
      <p>
        The agent drafts one request for everything this month needs from the client: each question says what answering it unblocks, and what needs the firm's own judgment is
        never asked. Reword it or add your own questions; nothing goes to the client until you approve.
      </p>
      <ProposedRequest closeId={closeId} />
      {none && (
        <>
          {kept === undefined ? (
            <button type="button" className="primary" disabled={busy} onClick={() => void draft([])}>
              {busy ? "Drafting…" : "Draft the request now"}
            </button>
          ) : (
            <p role="status">The client has nothing to answer for this month now.</p>
          )}
          {kept && kept.length > 0 && (
            <details>
              <summary>Kept with the firm, not asked of the client · {kept.length}</summary>
              <ul>
                {kept.map((k) => (
                  <li key={k}>{k}</li>
                ))}
              </ul>
            </details>
          )}
          <form
            onSubmit={(e: FormEvent<HTMLFormElement>) => {
              e.preventDefault();
              void draft([question]);
            }}
          >
            <label>
              Ask the client something yourself
              <textarea rows={2} maxLength={1000} value={question} onChange={(e) => setQuestion(e.target.value)} />
            </label>
            <button disabled={busy || !question.trim()}>Draft a request with it</button>
          </form>
        </>
      )}
      {error && <p role="alert">{error}</p>}
      {history.isError && <p role="alert">The saved request drafts could not be read. {history.error.message}</p>}
      {(history.data?.length ?? 0) > 0 && (
        <details>
          <summary>Saved request drafts · latest {history.data!.length}</summary>
          {history.data!.map((d) => (
            <article key={d.id}>
              <h3>
                Draft #{d.id} · {dates.dateTime(d.created_at)} · {d.created_by}
              </h3>
              <pre className="pre-line">{d.body}</pre>
            </article>
          ))}
        </details>
      )}
    </section>
  );
}
