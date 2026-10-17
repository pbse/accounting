import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { decisionRefreshMs, workspaceKey, workspaceQuery } from "../../data/queries";
import { workspaceRequest } from "./workspace";
import { PasscodeNotice, SendRequestEmail } from "./Email";

// Each question names what an answer lets the agent continue; kept is what the agent left with the firm, never sent.
type Proposal = { id: string; entity: string; period: string; recipient: string; questions: { document: string; question: string; unblocks?: string }[]; kept?: string[] };

// The client request the close's goal drafted, the same on the goal card, in chat and on the Requests tab: the client and
// month, the exact questions (their wording editable before approval), the recipient and the firm's sender. With a
// sender, one action approves the request and its email together, both approvals named in its confirmation; or the
// request alone, its email a second approval (or the link shared by hand).
export function ProposedRequest({ closeId, onApproved }: { closeId: string; onApproved?: () => void }) {
  // Read again as the agent works, so a request it drafts in the background appears without a reload.
  const read = useQuery({ ...workspaceQuery<{ proposal: Proposal | null }>(`/${closeId}/requests/proposed`), refetchInterval: decisionRefreshMs, refetchOnWindowFocus: true });
  const queries = useQueryClient();
  const email = useQuery(workspaceQuery<{ hosted?: boolean; connection?: { id: string; sender: string } | null }>(`/email`));
  const proposal = read.data?.proposal;
  // A draft the agent replaced while it was open is a different request: wording edits and the confirmation start again,
  // and the panel says it changed.
  const [shownId, setShownId] = useState<string>();
  const [replaced, setReplaced] = useState(false);
  const sender = email.data?.hosted && email.data.connection ? email.data.connection : null;
  const [recipient, setRecipient] = useState<string>();
  const [wording, setWording] = useState<Record<number, string>>({});
  const [editing, setEditing] = useState(false);
  const [alone, setAlone] = useState(false);
  const [added, setAdded] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<{ id: string; passcode: string; recipient: string; link: string; sent?: boolean }>();
  if (proposal && proposal.id !== shownId) {
    setReplaced(shownId !== undefined);
    setShownId(proposal.id);
    setWording({});
    setConfirmed(false);
    setAdded("");
  }
  if (opened) {
    return (
      <section aria-label="Approved client request">
        <p role="status">
          Request #{opened.id} is approved{opened.sent ? ` and its email is queued to ${opened.recipient}.` : " and its private link is ready. Nothing has been sent."}
        </p>
        {error && <p role="alert">{error}</p>}
        {opened.link && <p>Private link, to share yourself if you do not send the email: <code>{opened.link}</code></p>}
        <PasscodeNotice passcode={opened.passcode} />
        {!opened.sent && (
          // Queuing the email replaces the unused link with the one the email carries, and its passcode with a new one.
          <SendRequestEmail closeId={closeId} requestId={opened.id} recipient={opened.recipient} onQueued={(passcode) => setOpened({ ...opened, link: "", passcode, sent: true })} />
        )}
      </section>
    );
  }
  // With no draft to show, what still needs saying: the request could not be read, or an approval was refused because the
  // draft was approved elsewhere or changed (the read that followed found none).
  if (!proposal) {
    if (read.isError) return <p role="alert">The drafted request could not be read. {read.error.message}</p>;
    return error ? <p role="alert">{error}</p> : null;
  }
  const to = recipient ?? proposal.recipient;
  const send = !!sender && !alone;
  const questions = proposal.questions.map((q, i) => ({ document: q.document, question: wording[i] ?? q.question }));
  const kept = proposal.kept ?? [];
  // A question of the person's own joins the draft (a new draft, the drafted questions first), as every entry point shows it.
  async function add() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/requests/proposed`, { questions: [added] });
      setAdded("");
      await queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests/proposed`) });
      void queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests`) });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The question could not be added.");
    } finally {
      setBusy(false);
    }
  }
  async function approve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!proposal) return;
    setBusy(true);
    setError("");
    let result: { id: string; passcode: string; link_token: string };
    try {
      result = await workspaceRequest(`/${closeId}/requests/${proposal.id}/approve`, { recipient: to.trim(), confirmed, questions });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The request could not be approved.");
      void read.refetch();
      setBusy(false);
      return;
    }
    let done = { id: result.id, passcode: result.passcode, recipient: to.trim(), link: `${window.location.origin}/request#${result.link_token}`, sent: false };
    if (send && sender) {
      try {
        const queued = await workspaceRequest(`/${closeId}/requests/${result.id}/email`, { previous_id: "", connection_id: sender.id, confirmed: true });
        done = { ...done, link: "", passcode: queued.passcode ?? "", sent: true };
      } catch (e) {
        setError(`The request is approved, but its email was not queued: ${e instanceof Error ? e.message : "the sender could not be confirmed"}. Send it below, or share the private link yourself.`);
      }
    }
    setOpened(done);
    // Every surface showing the drafted request (goal card, chat, Requests tab) reads it again.
    void queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests/proposed`) });
    void queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests`) });
    onApproved?.();
    setBusy(false);
  }
  return (
    <form aria-label="Drafted client request" onSubmit={(e) => void approve(e)}>
      <h3>Drafted client request · {proposal.entity} · {proposal.period}</h3>
      <p>The agent drafted these questions from the month's documents, its ledger accounts and its drafts; questions added by the firm follow them. Nothing goes to the client until you approve.</p>
      {replaced && <p role="status">The agent updated this request since you opened it. Check it again before approving.</p>}
      <ol>
        {questions.map((q, i) => (
          <li key={i}>
            {q.document && (
              <>
                <strong>{q.document}</strong>:{" "}
              </>
            )}
            {editing ? (
              <textarea aria-label={`Question ${i + 1}, about ${q.document}`} value={q.question} maxLength={1000} rows={2} onChange={(e) => setWording({ ...wording, [i]: e.target.value })} />
            ) : (
              q.question
            )}
            {proposal.questions[i].unblocks && <small> · answering it unblocks {proposal.questions[i].unblocks}</small>}
          </li>
        ))}
      </ol>
      {kept.length > 0 && (
        <details>
          <summary>Kept with the firm, not asked of the client · {kept.length}</summary>
          <ul>
            {kept.map((k) => (
              <li key={k}>{k}</li>
            ))}
          </ul>
        </details>
      )}
      {editing ? (
        <p>
          <label>
            Add a question of your own
            {/* A text area, so Enter never submits the approval around it. */}
            <textarea rows={2} value={added} maxLength={1000} onChange={(e) => setAdded(e.target.value)} />
          </label>{" "}
          <button type="button" disabled={busy || !added.trim()} onClick={() => void add()}>
            Add to the request
          </button>
        </p>
      ) : (
        <button type="button" onClick={() => setEditing(true)}>
          Edit wording
        </button>
      )}
      <label>
        Recipient
        <input type="email" required value={to} onChange={(e) => setRecipient(e.target.value)} placeholder="client@example.com" />
      </label>
      {sender ? (
        <>
          <p>
            From: {sender.sender} · To: {to || "the recipient above"}. The email carries a private seven-day reply link; the questions stay on the request page.
          </p>
          <label>
            <input type="checkbox" checked={alone} onChange={(e) => setAlone(e.target.checked)} /> Approve the request only; I'll send the email or share the link later.
          </label>
        </>
      ) : (
        <p>No email sender yet; share the private link yourself, or set a sender in Settings.</p>
      )}
      <label>
        <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} required />{" "}
        {send ? `I approve this request, and its email from ${sender!.sender}, to this exact recipient.` : "I approve this request to this exact recipient. The email is a separate approval."}
      </label>
      {error && <p role="alert">{error}</p>}
      <button className="primary" disabled={busy || !confirmed || !to.trim() || questions.some((q) => !q.question.trim())}>
        {busy ? "Approving…" : send ? "Approve and send" : "Approve request"}
      </button>
    </form>
  );
}
