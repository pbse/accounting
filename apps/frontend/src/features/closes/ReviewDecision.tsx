import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";

type Review = {
  state: string;
  digest: string;
  review_id: string;
  history: {
    id: number;
    decision: string;
    note: string;
    actor: string;
    created_at: string;
  }[];
};
export function ReviewDecision({
  closeId,
  paperId,
  onChange,
  autoLoad = false,
}: {
  closeId: string;
  paperId: string;
  onChange: () => void;
  autoLoad?: boolean;
}) {
  const [review, setReview] = useState<Review>();
  const [comparison, setComparison] = useState<{
    previous_id: string;
    changes: { field: string; before: string; after: string }[];
  }>();
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    setBusy(true);
    setError("");
    try {
      const [decision, changes] = await Promise.all([
        workspaceRequest(`/${closeId}/workpapers/${paperId}/reviews`),
        workspaceRequest(`/${closeId}/workpapers/${paperId}/changes`),
      ]);
      setReview(decision);
      setComparison(changes);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Review unavailable.");
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    if (autoLoad) void load();
  }, [autoLoad]);
  async function decide(decision: string) {
    if (!review) return;
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/workpapers/${paperId}/reviews`, {
        decision,
        note,
        digest: review.digest,
        expected_review_id: review.review_id,
      });
      setNote("");
      onChange();
      if (autoLoad) void load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not save decision.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h3>Workpaper review</h3>
      <p>
        Review is a decision about this exact draft. It does not authorize
        posting or sign off the close. Local-token decisions are attributed to
        the shared development workspace.
      </p>
      {!autoLoad && (
        <button disabled={busy} onClick={() => void load()}>
          {review ? "Reload review" : "Review decision and history"}
        </button>
      )}
      {error && <p role="alert">{error}</p>}
      {review && (
        <>
          <p role="status">
            Current review: {review.state.replaceAll("_", " ")}
          </p>
          {review.state === "source_changed" && (
            <p>
              Evidence was replaced, is unavailable, or a revised draft
              supersedes this work. Compare the versions and review the current
              draft. Earlier decisions remain in history.
            </p>
          )}
          {comparison?.previous_id && (
            <section>
              <h4>Changes from draft #{comparison.previous_id}</h4>
              <p>
                Exact stored inputs and results. Independent workpapers are
                unaffected. Review changed evidence and assumptions before
                approving this revision.
              </p>
              {comparison.changes.length === 0 ? (
                <p>No calculation or source values changed.</p>
              ) : (
                <table>
                  <caption>
                    Previous and current draft · {comparison.changes.length}{" "}
                    changed fields
                  </caption>
                  <thead>
                    <tr>
                      <th scope="col">Field</th>
                      <th scope="col">Before</th>
                      <th scope="col">After</th>
                    </tr>
                  </thead>
                  <tbody>
                    {comparison.changes.map((c) => (
                      <tr key={c.field}>
                        <th scope="row">
                          {c.field.replaceAll("_", " ").replaceAll(".", " / ")}
                        </th>
                        {[c.before, c.after].map((value, i) => (
                          <td key={i}>
                            <pre
                              style={{
                                whiteSpace: "pre-wrap",
                                overflowWrap: "anywhere",
                                maxHeight: "16rem",
                                maxWidth: "28rem",
                                overflow: "auto",
                              }}
                            >
                              {value}
                            </pre>
                          </td>
                        ))}
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </section>
          )}
          <p>
            <Link to={`/dashboard/closes/${closeId}?tab=requests#client-request`}>
              Draft a consolidated request for information
            </Link>
          </p>
          <label>
            Review note
            <textarea
              value={note}
              maxLength={2000}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Explain what you checked or what needs changing."
            />
          </label>
          <div className="export-actions">
            <button
              disabled={
                busy || !note.trim() || review.state === "source_changed"
              }
              onClick={() => void decide("approved")}
            >
              Approve workpaper
            </button>
            <button
              disabled={busy || !note.trim()}
              onClick={() => void decide("changes_requested")}
            >
              Request changes
            </button>
          </div>
          <p>
            Approval requires linked current evidence and no unresolved
            calculation blockers. Hosted users need a reviewer role.
          </p>
          <h4>Decision history</h4>
          {review.history.length === 0 ? (
            <p>No decisions recorded.</p>
          ) : (
            <ol>
              {review.history.map((h) => (
                <li key={h.id}>
                  <strong>{h.decision.replaceAll("_", " ")}</strong> · {h.actor}{" "}
                  · {new Date(h.created_at).toLocaleString()}
                  <p>{h.note}</p>
                </li>
              ))}
            </ol>
          )}
        </>
      )}
    </section>
  );
}
