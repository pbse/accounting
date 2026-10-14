import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { workspaceKey, workspaceQuery } from "../../data/queries";
import { workspaceRequest } from "./workspace";
import { PasscodeNotice, SendRequestEmail } from "./Email";

type Proposal = { id: string; entity: string; period: string; recipient: string; questions: { document: string; question: string }[] };

// The client request the close's goal drafted, the same on the goal card, in chat and on the Requests tab: the client and
// month, the exact questions (their wording editable before approval), the recipient and the firm's sender. With a
// sender, one action approves the request and its email together, both approvals named in its confirmation; or the
// request alone, its email a second approval (or the link shared by hand).
export function ProposedRequest({ closeId, onApproved }: { closeId: string; onApproved?: () => void }) {
  const read = useQuery(workspaceQuery<{ proposal: Proposal | null }>(`/${closeId}/requests/proposed`));
  const queries = useQueryClient();
  const email = useQuery(workspaceQuery<{ hosted?: boolean; connection?: { id: string; sender: string } | null }>(`/email`));
  const proposal = read.data?.proposal;
  const sender = email.data?.hosted && email.data.connection ? email.data.connection : null;
  const [recipient, setRecipient] = useState<string>();
  const [wording, setWording] = useState<Record<number, string>>({});
  const [editing, setEditing] = useState(false);
  const [alone, setAlone] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<{ id: string; passcode: string; recipient: string; link: string; sent?: boolean }>();
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
  if (!proposal) return null;
  const to = recipient ?? proposal.recipient;
  const send = !!sender && !alone;
  const questions = proposal.questions.map((q, i) => ({ document: q.document, question: wording[i] ?? q.question }));
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
    onApproved?.();
    setBusy(false);
  }
  return (
    <form aria-label="Drafted client request" onSubmit={(e) => void approve(e)}>
      <h3>Drafted client request · {proposal.entity} · {proposal.period}</h3>
      <p>The agent drafted these questions from the month's documents. Nothing goes to the client until you approve.</p>
      <ol>
        {questions.map((q, i) => (
          <li key={i}>
            <strong>{q.document}</strong>:{" "}
            {editing ? (
              <textarea aria-label={`Question ${i + 1}, about ${q.document}`} value={q.question} maxLength={1000} rows={2} onChange={(e) => setWording({ ...wording, [i]: e.target.value })} />
            ) : (
              q.question
            )}
          </li>
        ))}
      </ol>
      {!editing && (
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
