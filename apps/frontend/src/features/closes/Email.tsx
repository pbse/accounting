import { useState, type FormEvent } from "react";
import { apiBaseUrl } from "../../lib/api";
import { workspaceRequest } from "./workspace";

type Sender = {
  id: string;
  sender: string;
  domain_id: string;
  verified_at: string;
  tracking_configured: boolean;
};
type Configuration = { connection: Sender | null; hosted: boolean };
export function EmailSettings() {
  const [data, setData] = useState<Configuration>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function load() {
    setBusy(true);
    setError("");
    try {
      setData(await workspaceRequest("/email"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sender unavailable.");
    } finally {
      setBusy(false);
    }
  }
  async function save(event: FormEvent<HTMLFormElement>, disable: boolean) {
    event.preventDefault();
    const form = event.currentTarget;
    const values = new FormData(form);
    setBusy(true);
    setError("");
    try {
      await workspaceRequest("/email", {
        key: values.get("key") ?? "",
        from: values.get("from") ?? "",
        domain_id: values.get("domain_id") ?? "",
        previous_id: data?.connection?.id ?? "",
        confirmed: values.get("confirmed") === "on",
        disable,
      });
      form.reset();
      await load();
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Sender change could not be confirmed.",
      );
    } finally {
      setBusy(false);
    }
  }
  async function tracking(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); const form=event.currentTarget; const values=new FormData(form);
    setBusy(true); setError("");
    try { await workspaceRequest("/email",{previous_id:data?.connection?.id,webhook_secret:values.get("secret"),confirmed:values.has("confirmed")}); form.reset(); await load(); }
    catch(e) { setError(e instanceof Error ? e.message : "Tracking setup failed."); }
    finally { setBusy(false); }
  }
  return (
    <details
      onToggle={(e) => {
        if (e.currentTarget.open && !data && !busy) void load();
      }}
    >
      <summary>Verified firm email sender</summary>
      <p>
        Connect the firm’s verified Resend domain. The API key is encrypted on
        the server and never returned to this page. Connecting a sender does not
        authorize any email.
      </p>
      {error && <p role="alert">{error}</p>}
      <button disabled={busy} onClick={() => void load()}>
        Refresh sender
      </button>
      {data && (
        <>
          <p>
            {data.connection
              ? `Current sender: ${data.connection.sender}. Domain last checked ${new Date(data.connection.verified_at).toLocaleString()}.`
              : "No email sender connected. Private links can still be shared manually."}
          </p>
          {!data.hosted ? (
            <p>
              Live sending requires a WorkOS organization session. Use manual
              links for local development; provider behavior is covered by
              synthetic checks.
            </p>
          ) : (
            <>
              <form
                key={data.connection?.id ?? "new"}
                onSubmit={(e) => void save(e, false)}
              >
                <fieldset disabled={busy}>
                  <legend>Connect or replace the sender</legend>
                  <label>
                    Verified sender (email or Firm name &lt;email&gt;)
                    <input
                      name="from"
                      required
                      maxLength={320}
                      defaultValue={data.connection?.sender ?? ""}
                    />
                  </label>
                  <label>
                    Resend domain ID
                    <input
                      name="domain_id"
                      required
                      maxLength={100}
                      defaultValue={data.connection?.domain_id ?? ""}
                    />
                  </label>
                  <label>
                    Resend API key
                    <input
                      name="key"
                      required
                      type="password"
                      autoComplete="off"
                    />
                  </label>
                  <p>
                    The key needs sender-domain read and email-send access.
                    Disable click tracking on the sending domain to keep private
                    links direct. API and worker also need the same server-side
                    SECRETS_KEY; the API needs PUBLIC_APP_URL.
                  </p>
                  <label>
                    <input name="confirmed" type="checkbox" required />I am
                    authorized to connect this firm’s Resend account and sender.
                    Replacing a connection stops pending attempts using the
                    previous connection.
                  </label>
                  <button>Verify domain and save connection</button>
                </fieldset>
              </form>
              {data.connection && <form onSubmit={e=>void tracking(e)}><fieldset disabled={busy}>
                <legend>Delivery tracking · {data.connection.tracking_configured ? "configured" : "setup required"}</legend>
                <p>In Resend, create a webhook for email.sent, email.delivered, email.delivery_delayed, email.bounced, email.complained, email.failed and email.suppressed at:</p>
                <code>{apiBaseUrl || window.location.origin}/v1/webhooks/resend/{data.connection.id}</code>
                <label>Webhook signing secret<input name="secret" type="password" autoComplete="off" required /></label>
                <label><input name="confirmed" type="checkbox" required /> This webhook belongs to this firm’s Resend account.</label>
                <button>Save signing secret</button>
              </fieldset></form>}
              {data.connection && (
                <form onSubmit={(e) => void save(e, true)}>
                  <label>
                    <input type="checkbox" name="confirmed" required />
                    Disconnect this sender. Already accepted or in-flight emails
                    cannot be recalled.
                  </label>
                  <button disabled={busy}>Disconnect sender</button>
                </form>
              )}
            </>
          )}
        </>
      )}
    </details>
  );
}

// The passcode exists only in this response; the firm tells the client another way.
export function PasscodeNotice({ passcode }: { passcode: string }) {
  if (!passcode) return null;
  return (
    <p role="status">
      Passcode for this link: <strong>{passcode.slice(0, 4)} {passcode.slice(4)}</strong>. Tell the client by phone or in person, never in the same
      email. It is shown only now; if it is lost, issue a new link.
    </p>
  );
}

export function SendRequestEmail({
  closeId,
  jobId,
  requestId,
  revision,
  recipient,
  onQueued,
  previousId,
  reminder,
}: {
  closeId: string;
  jobId?: string;
  requestId: string;
  revision?: number;
  previousId?: string;
  recipient: string;
  onQueued: (passcode: string) => void;
  reminder?: ReminderStatus;
}) {
  // The firm's rule decides when a reminder is due and how many are allowed; each still needs this approval.
  const stop = !previousId || !reminder ? "" : reminder.left <= 0
    ? "This request is unanswered after all the reminders the firm's rule allows. Contact the client another way."
    : reminder.expires_at && (new Date(reminder.expires_at) <= new Date() || (reminder.due_at && new Date(reminder.due_at) >= new Date(reminder.expires_at)))
      ? "The private link has expired, or expires before the next reminder is due. Contact the client another way."
      : "";
  const waiting = previousId && reminder?.due_at && new Date(reminder.due_at) > new Date() ? `Under the firm's rule the next reminder is due ${new Date(reminder.due_at).toLocaleString()}.` : "";
  const [data, setData] = useState<Configuration>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function load() {
    setBusy(true);
    setError("");
    try {
      setData(await workspaceRequest("/email"));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sender unavailable.");
    } finally {
      setBusy(false);
    }
  }
  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(
        `/${closeId}${jobId ? `/work/${jobId}` : ""}/requests/${requestId}/email`,
        {
          revision,
          previous_id: previousId ?? "",
          connection_id: data?.connection?.id,
          confirmed:
            new FormData(event.currentTarget).get("confirmed") === "on",
        },
      );
      onQueued(result.passcode ?? "");
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Delivery approval could not be confirmed. Refresh Work before retrying.",
      );
    } finally {
      setBusy(false);
    }
  }
  if (stop) return <p role="status">{stop}</p>;
  return (
    <details
      onToggle={(e) => {
        if (e.currentTarget.open && !data && !busy) void load();
      }}
    >
      <summary>{previousId ? "Approve a reminder" : "Send the approved request via Resend"}</summary>
      {error && <p role="alert">{error}</p>}
      {data?.hosted && data.connection ? (
        <form onSubmit={(e) => void send(e)}>
          <fieldset disabled={busy}>
            <legend>Approve this email</legend>
            <p>
              From: {data.connection.sender}
              <br />
              To: {recipient}
            </p>
            <p>Subject: {previousId ? "Reminder: a secure request from your accounting team" : "A secure request from your accounting team"}</p>
            <p>
              {previousId ? "This reminder reuses the original private link and expiry. It does not extend access. A reply, changed scope or adverse delivery event blocks sending." : "The email contains a private seven-day reply link and generic instructions. Accounting details remain on the request page. Any previous private link for this request will be revoked."}
            </p>
            <label>
              <input name="confirmed" type="checkbox" required />I approve
              sending this private request link through Resend from this sender
              to this exact recipient.
            </label>
            {waiting && <p role="status">{waiting}</p>}
            <button disabled={!!waiting}>{previousId ? "Approve and queue this reminder" : "Approve and queue email"}</button>
          </fieldset>
        </form>
      ) : (
        data && (
          <p>
            Connect a verified firm sender from Client closes and use a hosted
            session with sending permission. Manual link sharing remains
            available.
          </p>
        )
      )}
      <p>
        Provider acceptance does not prove delivery. Unknown outcomes require
        provider investigation before creating another request; do not blindly
        resend.
      </p>
    </details>
  );
}

export type ReminderStatus = { left: number; due_at?: string; expires_at?: string };
export type EmailRecord = { id: string; state: string; delivery: string; reminder_of?: string; error: string; approved_by?: string; created_at: string };
export function EmailHistory({emails}: {emails: EmailRecord[]}) {
  return <section aria-label="Email delivery history"><h5>Email history</h5>{emails.map(m=><p key={m.id}>
    {m.reminder_of ? "Reminder" : "Initial email"} · {new Date(m.created_at).toLocaleString()} · {m.state.replaceAll("_"," ")} · delivery: {m.delivery.replaceAll("_"," ")}{m.approved_by ? ` · approved by ${m.approved_by}` : ""}. {m.error}
  </p>)}<p>Delivered means accepted by the recipient’s mail server, not read. Reminders require a separate approval after confirmed delivery. Replies and delivery failures block reminders.</p></section>;
}
