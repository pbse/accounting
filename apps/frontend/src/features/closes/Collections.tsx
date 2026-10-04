import { type FormEvent, useEffect, useState } from "react";
import { money, workspaceRequest } from "./workspace";
import { SettleEmail } from "./Email";

type Invoice = { id: string; label: string; balance: string; currency: string; due: string };
type Message = {
  id: string;
  label: string;
  amount: string;
  currency: string;
  recipient: string;
  subject: string;
  body: string;
  email: { id: string; state: string; delivery: string; error: string } | null;
};
type State = {
  authority: { granted: boolean; sender: string; reference: string; recorded_by: string; recorded_at: string } | null;
  messages: Message[];
  preferences: { recipient: string; do_not_contact: boolean; reason: string }[];
  bounced: string[];
};

// Collection emails to the client's customers: only under the client's recorded authority, never to an address the
// client asked us not to email or one that bounced, each message read and approved by a person before it is sent.
export function Collections({ closeId, invoices, refresh }: { closeId: string; invoices: Invoice[]; refresh: number }) {
  const [state, setState] = useState<State>();
  const [sender, setSender] = useState<{ id: string; sender: string } | null>(null);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    Promise.all([workspaceRequest(`/${closeId}/collections`), workspaceRequest("/email")])
      .then(([data, email]) => live && (setState(data), setSender(email.connection), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Collections are unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, version, refresh]);
  async function act(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/collections`, body);
      setSaved(done);
      setVersion((n) => n + 1);
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : "That could not be saved.");
      return false;
    } finally {
      setBusy(false);
    }
  }
  function submit(event: FormEvent<HTMLFormElement>, body: (form: FormData) => Record<string, unknown>, done: string) {
    event.preventDefault();
    const element = event.currentTarget;
    void act(body(new FormData(element)), done).then((ok) => ok && element.reset());
  }
  if (!state) return error ? <p role="alert">{error}</p> : null;
  const authority = state.authority;
  const otherSender = authority?.granted && sender && authority.sender !== sender.sender;
  const blocked = (recipient: string) =>
    state.bounced.includes(recipient.toLowerCase()) ? "bounced before" : state.preferences.find((p) => p.recipient.toLowerCase() === recipient.toLowerCase() && p.do_not_contact) ? "asked not to be emailed" : "";
  return (
    <section aria-label="Collection emails">
      <h3>Collection emails</h3>
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">{saved}</p>}
      <p className="note">
        {authority?.granted
          ? `The client authorized contacting its customers from ${authority.sender} (${authority.reference}), recorded by ${authority.recorded_by}.${otherSender ? ` The firm's sender is now ${sender.sender}: record the client's authority for it before sending.` : ""}`
          : authority
            ? `The client withdrew authority to contact its customers (${authority.reference}); nothing can be sent.`
            : "No authority from the client to contact its customers is recorded; nothing can be sent."}{" "}
        Messages go from that sender, each approved by a person; the outbox checks the authority, the address and the books again just before sending, and a new
        capture stops a message not yet sent. Nothing here records a payment, a promise or a write-off.
      </p>
      <details>
        <summary>Record the client's decision</summary>
        <form onSubmit={(e) => submit(e, (f) => ({ action: "authority", granted: f.get("granted") === "yes", reference: f.get("reference"), confirmed: f.has("confirmed") }), "Recorded the client's decision.")}>
          <fieldset disabled={busy}>
            <label>
              The client
              <select name="granted" defaultValue="yes">
                <option value="yes">authorizes us to contact its customers about money owed{sender ? ` from ${sender.sender}` : ""}</option>
                <option value="no">does not, or withdraws that authority</option>
              </select>
            </label>
            <label>
              Where they said so
              <input name="reference" required minLength={5} maxLength={2000} placeholder="Engagement letter, section 4" />
            </label>
            <label>
              <input type="checkbox" name="confirmed" required /> This is the client's own decision.
            </label>
            <button>Record</button>
          </fieldset>
        </form>
      </details>
      {invoices.length > 0 && (
        <details>
          <summary>Draft a collection email</summary>
          <form onSubmit={(e) => submit(e, (f) => Object.fromEntries([...f].map(([k, v]) => [k, String(v).trim()]).concat([["action", "draft"]])), "Drafted. Read it, then approve it to send.")}>
            <fieldset disabled={busy}>
              <label>
                Invoice
                <select name="item_id" required>
                  {invoices.map((i) => (
                    <option key={i.id} value={i.id}>
                      {i.label} · {money(i.balance, i.currency)}
                      {i.due ? ` · due ${i.due}` : ""}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Customer contact's email (from the client)
                <input name="recipient" type="email" required maxLength={320} />
              </label>
              <label>
                Reply-to address (optional, for example the client's own)
                <input name="reply_to" type="email" maxLength={320} />
              </label>
              <label>
                Subject (optional; a plain one is written for you)
                <input name="subject" maxLength={200} />
              </label>
              <label>
                Message (optional; a plain one naming the invoice, its balance and due date is written for you)
                <textarea name="body" maxLength={5000} />
              </label>
              <button>Draft</button>
            </fieldset>
          </form>
        </details>
      )}
      {state.messages.length > 0 && (
        <ul>
          {state.messages.map((m) => {
            const reason = blocked(m.recipient);
            return (
              <li key={m.id}>
                <strong>{m.label}</strong> to {m.recipient} · {money(m.amount, m.currency)} ·{" "}
                {m.email ? (
                  <>
                  <span className={m.email.delivery === "delivered" ? "status ok" : m.email.state === "cancelled" || ["bounced", "complained", "suppressed", "failed"].includes(m.email.delivery) ? "status danger" : "status info"}>
                    {m.email.state === "cancelled" ? "Not sent: cancelled before sending" : `${m.email.state} · ${m.email.delivery}`}
                  </span>
                  {m.email.state === "unknown" && <SettleEmail closeId={closeId} emailId={m.email.id} />}
                  </>
                ) : (
                  <span className="status warn">Draft</span>
                )}
                {m.email?.error && <small> · {m.email.error}</small>}
                {reason && <small> · this address {reason}</small>}
                <details>
                  <summary aria-label={`Read the message to ${m.recipient} about ${m.label}`}>Read</summary>
                  <p>
                    <strong>{m.subject}</strong>
                  </p>
                  <p style={{ whiteSpace: "pre-wrap" }}>{m.body}</p>
                  {!m.email && sender && (
                    <form onSubmit={(e) => submit(e, (f) => ({ action: "approve", message_id: m.id, connection_id: sender.id, confirmed: f.has("confirmed") }), `Approved the message to ${m.recipient}; the outbox sends it.`)}>
                      <fieldset disabled={busy || !authority?.granted || !!otherSender || !!reason}>
                        <label>
                          <input type="checkbox" name="confirmed" required /> I read this message; send it to {m.recipient} from {sender.sender}.
                        </label>
                        <button className="primary">Approve and send</button>
                      </fieldset>
                    </form>
                  )}
                </details>
              </li>
            );
          })}
        </ul>
      )}
      <details>
        <summary>Contact preferences{state.bounced.length > 0 ? ` · ${state.bounced.length} bounced` : ""}</summary>
        {state.bounced.length > 0 && <p className="note">Bounced, marked as spam or suppressed, so not emailed again: {state.bounced.join(", ")}.</p>}
        <ul>
          {state.preferences.map((p) => (
            <li key={p.recipient}>
              {p.recipient}: {p.do_not_contact ? "do not email" : "may be emailed"} ({p.reason})
            </li>
          ))}
        </ul>
        <form onSubmit={(e) => submit(e, (f) => ({ action: "preference", recipient: f.get("recipient"), do_not_contact: f.get("choice") === "no", reason: f.get("reason") }), "Saved the preference.")}>
          <fieldset disabled={busy}>
            <label>
              Address
              <input name="recipient" type="email" required maxLength={320} />
            </label>
            <label>
              <select name="choice" defaultValue="no">
                <option value="no">Do not email</option>
                <option value="yes">May be emailed again</option>
              </select>
            </label>
            <label>
              Why
              <input name="reason" required maxLength={2000} />
            </label>
            <button>Save</button>
          </fieldset>
        </form>
      </details>
    </section>
  );
}
