import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { workspaceKey, workspaceQuery } from "../../data/queries";
import { workspaceRequest } from "./workspace";
import { PasscodeNotice, SendRequestEmail } from "./Email";

type Proposal = { id: string; entity: string; period: string; recipient: string; questions: { document: string; question: string }[] };

// The client request the close's goal drafted, the same on the goal card, in chat and on the Requests tab: the client and
// month, the exact questions, the recipient and the firm's sender. Approving it opens the request with a private link;
// sending its email is a second, explicit approval of that sender and recipient.
export function ProposedRequest({ closeId, onApproved }: { closeId: string; onApproved?: () => void }) {
  const read = useQuery(workspaceQuery<{ proposal: Proposal | null }>(`/${closeId}/requests/proposed`));
  const queries = useQueryClient();
  const email = useQuery(workspaceQuery<{ hosted?: boolean; connection?: { sender: string } | null }>(`/email`));
  const proposal = read.data?.proposal;
  const [recipient, setRecipient] = useState<string>();
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [opened, setOpened] = useState<{ id: string; passcode: string; recipient: string; link: string; sent?: boolean }>();
  if (opened) {
    return (
      <section aria-label="Approved client request">
        <p role="status">Request #{opened.id} is approved and its private link is ready.{opened.sent ? "" : " Nothing has been sent."}</p>
        {opened.link && <p>Private link, to share yourself if you do not send the email: <code>{opened.link}</code></p>}
        <PasscodeNotice passcode={opened.passcode} />
        {opened.sent ? <p role="status">The email is queued to {opened.recipient}.</p> : (
          // Queuing the email replaces the unused link with the one the email carries, and its passcode with a new one.
          <SendRequestEmail closeId={closeId} requestId={opened.id} recipient={opened.recipient} onQueued={(passcode) => setOpened({ ...opened, link: "", passcode, sent: true })} />
        )}
      </section>
    );
  }
  if (!proposal) return null;
  const to = recipient ?? proposal.recipient;
  async function approve(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!proposal) return;
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/requests/${proposal.id}/approve`, { recipient: to.trim(), confirmed });
      setOpened({ id: result.id, passcode: result.passcode, recipient: to.trim(), link: `${window.location.origin}/request#${result.link_token}` });
      // Every surface showing the drafted request (goal card, chat, Requests tab) reads it again.
      void queries.invalidateQueries({ queryKey: workspaceKey(`/${closeId}/requests/proposed`) });
      onApproved?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The request could not be approved.");
      void read.refetch();
    } finally {
      setBusy(false);
    }
  }
  return (
    <form aria-label="Drafted client request" onSubmit={(e) => void approve(e)}>
      <h3>Drafted client request · {proposal.entity} · {proposal.period}</h3>
      <p>The agent drafted these questions from the month's documents. Nothing goes to the client until you approve the request, then its email.</p>
      <ol>
        {proposal.questions.map((q, i) => <li key={i}><strong>{q.document}</strong>: {q.question}</li>)}
      </ol>
      <label>
        Recipient
        <input type="email" required value={to} onChange={(e) => setRecipient(e.target.value)} placeholder="client@example.com" />
      </label>
      <p>From: {email.data?.hosted && email.data.connection ? email.data.connection.sender : "No email sender yet; share the private link yourself, or set a sender in Settings."}</p>
      <label><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} required /> I approve this request to this exact recipient. The email is a separate approval.</label>
      {error && <p role="alert">{error}</p>}
      <button className="primary" disabled={busy || !confirmed || !to.trim()}>Approve request</button>
    </form>
  );
}
