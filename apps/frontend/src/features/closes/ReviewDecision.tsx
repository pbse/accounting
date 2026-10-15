import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";

type Review = {
  state: string;
  digest: string;
  review_id: string;
  // The agent's question about the latest return, while it waits for an answer.
  clarification?: string | null;
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
  payroll = false,
}: {
  closeId: string;
  paperId: string;
  onChange: () => void;
  autoLoad?: boolean;
  // A payroll recording is approved only with both of its confirmations, which the reviewer gives here.
  payroll?: boolean;
}) {
  const dates = useDates();
  // The decision, the comparison with the previous version and what changed since the last review: read with the page
  // when the panel opens by itself, otherwise when the person asks.
  const [asked, setAsked] = useState(autoLoad);
  useEffect(() => {
    if (autoLoad) setAsked(true);
  }, [autoLoad]);
  const base = `/${closeId}/workpapers/${paperId}`;
  const decision = useQuery({ ...workspaceQuery<Review>(`${base}/reviews`), enabled: asked });
  const changes = useQuery({ ...workspaceQuery<{ previous_id: string; changes: { field: string; before: string; after: string }[] }>(`${base}/changes`), enabled: asked });
  const sinceReview = useQuery({ ...workspaceQuery<{ last_review: null | { workpaper_id: string; decision: string; at: string }; changes: { field: string; before: string; after: string }[]; reasons: string[] }>(`${base}/since-review`), enabled: asked });
  const review = decision.data;
  const comparison = changes.data;
  const since = sinceReview.data;
  const [note, setNote] = useState("");
  const [answer, setAnswer] = useState("");
  const [finalSummary, setFinalSummary] = useState(false);
  const [notBooked, setNotBooked] = useState(false);
  const [actionError, setError] = useState("");
  const readFailed = decision.error ?? changes.error ?? sinceReview.error;
  const error = actionError || (readFailed ? readFailed.message || "Review unavailable." : "");
  const [acting, setBusy] = useState(false);
  const busy = acting || decision.isFetching || changes.isFetching || sinceReview.isFetching;
  async function load() {
    setError("");
    setAsked(true);
    await Promise.all([decision.refetch(), changes.refetch(), sinceReview.refetch()]);
  }
  // The answer resumes the return: the agent revises the draft with it on its next look.
  async function answerQuestion() {
    if (!review) return;
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/workpapers/${paperId}/clarification`, { review_id: review.review_id, answer });
      setAnswer("");
      onChange();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The answer could not be saved.");
    } finally {
      setBusy(false);
    }
  }
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
        ...(payroll && decision === "approved" ? { attestations: ["final_approved_summary", "not_already_booked"] } : {}),
      });
      setNote("");
      setFinalSummary(false);
      setNotBooked(false);
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
          {review.state === "changes_requested" && review.clarification && (
            <section aria-label="The agent's question">
              <h4>The agent asks before revising it</h4>
              <p>{review.clarification}</p>
              <label>
                Your answer
                <textarea value={answer} maxLength={2000} onChange={(e) => setAnswer(e.target.value)} />
              </label>
              <button className="primary" disabled={busy || !answer.trim()} onClick={() => void answerQuestion()}>
                Answer and let the agent revise it
              </button>
              <p className="note">Or add the document it needs to the close; the agent revises the draft once it has read it.</p>
            </section>
          )}
          {review.state === "source_changed" && (
            <p>
              Evidence was replaced, is unavailable, or a revised draft
              supersedes this work. Compare the versions and review the current
              draft. Earlier decisions remain in history.
            </p>
          )}
          {since && (since.last_review || since.reasons.length > 0) && (
            <section aria-label="Since your last review">
              <h4>Since your last review</h4>
              {since.last_review ? (
                <p>
                  You {since.last_review.decision.replaceAll("_", " ")} draft #{since.last_review.workpaper_id} on {dates.dateTime(since.last_review.at)}.
                  {since.last_review.workpaper_id !== paperId && ` ${since.changes.length} field${since.changes.length === 1 ? "" : "s"} changed since then.`}
                </p>
              ) : (
                <p>You have not reviewed this draft or its earlier versions.</p>
              )}
              {since.reasons.length > 0 && (
                <ul>
                  {since.reasons.map((r) => (
                    <li key={r}>{r}</li>
                  ))}
                </ul>
              )}
              {since.last_review && since.last_review.workpaper_id !== paperId && since.changes.length > 0 && (
                <table>
                  <caption>Your reviewed draft and this one</caption>
                  <thead>
                    <tr>
                      <th scope="col">Field</th>
                      <th scope="col">You reviewed</th>
                      <th scope="col">Now</th>
                    </tr>
                  </thead>
                  <tbody>
                    {since.changes.map((c) => (
                      <tr key={c.field}>
                        <th scope="row">{c.field.replaceAll("_", " ").replaceAll(".", " / ")}</th>
                        <td>{c.before}</td>
                        <td>{c.after}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </section>
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
                            <pre className="scroll-text">
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
              placeholder="What needs changing (required to request changes; optional to approve)."
            />
          </label>
          {payroll && (
            <fieldset>
              <legend>Approving this payroll recording, I confirm:</legend>
              <label><input type="checkbox" checked={finalSummary} onChange={(e) => setFinalSummary(e.target.checked)} /> The summary is the provider's final, approved payroll for the stated pay dates, not a preview.</label>
              <label><input type="checkbox" checked={notBooked} onChange={(e) => setNotBooked(e.target.checked)} /> This payroll is not already booked in the ledger.</label>
            </fieldset>
          )}
          <div className="export-actions">
            <button
              disabled={
                busy || review.state === "source_changed" || (payroll && !(finalSummary && notBooked))
              }
              onClick={() => void decide("approved")}
            >
              {payroll ? "Approve payroll recording" : "Approve workpaper"}
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
                  · {dates.dateTime(h.created_at)}
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
