import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates, useNow } from "../../data/time";
import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { apiBaseUrl } from "../../lib/api";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceRequest } from "./workspace";

type Sender = {
  id: string;
  // "krith" for Krith's own sender, every firm's default; otherwise the integration the firm connected.
  provider: string;
  sender: string;
  domain_id: string;
  verified_at: string;
  tracking_configured: boolean;
};
type Provider = { name: string; label: string };
// providers are the integrations the firm may connect instead of Krith's sender; krith says this server has Krith's.
type Configuration = { connection: Sender | null; hosted: boolean; providers?: Provider[]; krith?: boolean };

// What each integration asks for when the firm connects it. A new integration adds its fields here, named as its
// provider reads them (delivery.Provider.Connect).
const providerFields: Record<string, ReactNode> = {
  resend: (
    <>
      <label>
        Verified sender (email or Firm name &lt;email&gt;)
        <input name="from" required maxLength={320} />
      </label>
      <label>
        Resend domain ID
        <input name="domain_id" required maxLength={100} />
      </label>
      <label>
        Resend API key
        <input name="key" required type="password" autoComplete="off" />
      </label>
      <p>The key needs sender-domain read and email-send access. Disable click tracking on the sending domain to keep private links direct.</p>
    </>
  ),
};

// The firm's sender: Krith's own unless the firm connected one, its state in view, and each change (connect or replace,
// tracking, going back to Krith's) in a dialog.
export function EmailSettings() {
  const dates = useDates();
  // The firm's sender, read with the page (Settings' loader) and again after a change.
  const read = useQuery(workspaceQuery<Configuration>("/email"));
  const data = read.data;
  const [busy, setBusy] = useState(false);
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "Sender unavailable." : "");
  const [dialog, setDialog] = useState<"sender" | "tracking" | "disconnect" | null>(null);
  // A save leaves the form busy until the sender has been read again.
  const load = async () => {
    try {
      await read.refetch();
    } finally {
      setBusy(false);
    }
  };
  // The API's address: the configured one, or this page's own, known only in the browser.
  const [origin, setOrigin] = useState(apiBaseUrl);
  useEffect(() => {
    if (!apiBaseUrl) setOrigin(window.location.origin);
  }, []);
  const open = (which: typeof dialog) => (setError(""), setDialog(which));
  async function save(event: FormEvent<HTMLFormElement>, disable: boolean) {
    event.preventDefault();
    const form = event.currentTarget;
    const values = new FormData(form);
    // Everything the integration's form asked for goes to its provider as it was entered.
    const fields: Record<string, string> = {};
    values.forEach((value, name) => {
      if (name !== "provider" && name !== "confirmed" && typeof value === "string") fields[name] = value;
    });
    setBusy(true);
    setError("");
    try {
      await workspaceRequest("/email", {
        ...(disable ? {} : { provider: values.get("provider") ?? "", fields }),
        previous_id: data?.connection?.id ?? "",
        confirmed: values.get("confirmed") === "on",
        disable,
      });
      form.reset();
      setDialog(null);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Sender change could not be confirmed.");
      setBusy(false);
    }
  }
  async function tracking(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const values = new FormData(form);
    setBusy(true);
    setError("");
    try {
      await workspaceRequest("/email", { previous_id: data?.connection?.id, webhook_secret: values.get("secret"), confirmed: values.has("confirmed") });
      form.reset();
      setDialog(null);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Tracking setup failed.");
      setBusy(false);
    }
  }
  const connection = data?.connection;
  const krith = connection?.provider === "krith";
  const providers = data?.providers ?? [];
  const [chosen, setChosen] = useState("");
  const provider = providers.find((p) => p.name === chosen) ?? providers[0];
  return (
    <section aria-label="Email sender">
      <SectionHeader title="Email sender">
        {data?.hosted && providers.length > 0 && (
          <button type="button" className={connection ? "" : "primary"} onClick={() => open("sender")}>
            {connection && !krith ? "Replace the sender" : "Use the firm's own sender"}
          </button>
        )}
      </SectionHeader>
      <p>
        Client requests, reminders and collection messages go out from Krith's sender in the firm's name, or from a sender the firm connects instead. A
        firm's own key is encrypted on the server and never returned to this page. Every email is still approved by a person.
      </p>
      {!data ? (
        error ? (
          <p role="alert">
            {error}{" "}
            <button type="button" disabled={busy} onClick={() => void load()}>
              Try again
            </button>
          </p>
        ) : (
          <p role="status">Reading the sender…</p>
        )
      ) : !data.hosted ? (
        <p className="note">Live sending needs the firm's hosted sign-in, which local development does not have. Share links by hand instead.</p>
      ) : !connection ? (
        <p className="empty">No email sender. Krith's sender is not set up on this server; connect the firm's own, or share private links by hand.</p>
      ) : (
        <RowList>
          <div className="row">
            <span>
              <strong>{connection.sender}</strong>
              <small>
                {krith
                  ? data.krith
                    ? "Krith's sender, in the firm's name. Clients' replies to requests come back to the close."
                    : "Krith's sender, which is not set up on this server: nothing goes out until it is, or until the firm connects its own."
                  : `Domain last checked ${dates.dateTime(connection.verified_at)}`}
              </small>
            </span>
            <span className={`pill ${connection.tracking_configured ? "" : "muted"}`}>{connection.tracking_configured ? "Delivery tracked" : "Tracking not set up"}</span>
            {!krith && (
              <span>
                <button type="button" onClick={() => open("tracking")}>
                  {connection.tracking_configured ? "Change tracking" : "Set up tracking"}
                </button>
              </span>
            )}
            {!krith && (
              <button type="button" onClick={() => open("disconnect")}>
                {data.krith ? "Use Krith's sender" : "Disconnect"}
              </button>
            )}
          </div>
        </RowList>
      )}
      {error && data && !dialog && <p role="alert">{error}</p>}
      <Dialog
        open={dialog === "sender"}
        onClose={() => setDialog(null)}
        title={connection && !krith ? "Replace the sender" : "Use the firm's own sender"}
        description="Emails approved from then on go from the firm's own account instead of Krith's sender."
      >
        <form key={`${connection?.id ?? "new"}:${provider?.name ?? ""}`} onSubmit={(e) => void save(e, false)}>
          <fieldset disabled={busy}>
            {providers.length > 1 ? (
              <label>
                Send through
                <select name="provider" value={provider?.name ?? ""} onChange={(e) => setChosen(e.target.value)}>
                  {providers.map((p) => (
                    <option key={p.name} value={p.name}>
                      {p.label}
                    </option>
                  ))}
                </select>
              </label>
            ) : (
              <input type="hidden" name="provider" value={provider?.name ?? ""} />
            )}
            {provider && providerFields[provider.name]}
            <label className="checkbox-label">
              <input name="confirmed" type="checkbox" required />I am authorized to connect this firm’s {provider?.label ?? ""} account and sender. Replacing a connection stops pending
              attempts using the previous connection.
            </label>
            {error && <p role="alert">{error}</p>}
            <DialogActions>
              <button type="button" onClick={() => setDialog(null)}>
                Cancel
              </button>
              <button className="primary">Verify and save</button>
            </DialogActions>
          </fieldset>
        </form>
      </Dialog>
      {connection && (
        <Dialog open={dialog === "tracking"} onClose={() => setDialog(null)} title="Delivery tracking" description="Resend tells this server what became of each email through a signed webhook.">
          <form onSubmit={(e) => void tracking(e)}>
            <fieldset disabled={busy}>
              <p>In Resend, create a webhook for email.sent, email.delivered, email.delivery_delayed, email.bounced, email.complained, email.failed and email.suppressed at:</p>
              <code>{origin}/v1/webhooks/resend/{connection.id}</code>
              <label>
                Webhook signing secret
                <input name="secret" type="password" autoComplete="off" required />
              </label>
              <label className="checkbox-label">
                <input name="confirmed" type="checkbox" required /> This webhook belongs to this firm’s Resend account.
              </label>
              {error && <p role="alert">{error}</p>}
              <DialogActions>
                <button type="button" onClick={() => setDialog(null)}>
                  Cancel
                </button>
                <button className="primary">Save signing secret</button>
              </DialogActions>
            </fieldset>
          </form>
        </Dialog>
      )}
      {connection && (
        <Dialog
          open={dialog === "disconnect"}
          onClose={() => setDialog(null)}
          title={data?.krith ? "Use Krith's sender" : "Disconnect the sender"}
          description={`${data?.krith ? "Emails approved from then on go from Krith's sender in the firm's name." : "No email goes out until a sender is connected again."} Already accepted or in-flight emails cannot be recalled.`}
        >
          <form onSubmit={(e) => void save(e, true)}>
            <fieldset disabled={busy}>
              <label className="checkbox-label">
                <input type="checkbox" name="confirmed" required />
                Disconnect {connection.sender}.
              </label>
              {error && <p role="alert">{error}</p>}
              <DialogActions>
                <button type="button" onClick={() => setDialog(null)}>
                  Cancel
                </button>
                <button className="primary">Disconnect sender</button>
              </DialogActions>
            </fieldset>
          </form>
        </Dialog>
      )}
    </section>
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
  const dates = useDates();
  const now = new Date(useNow());
  // The firm's rule decides when a reminder is due and how many are allowed; each still needs this approval.
  const stop = !previousId || !reminder ? "" : reminder.left <= 0
    ? "This request is unanswered after all the reminders the firm's rule allows. Contact the client another way."
    : reminder.expires_at && (new Date(reminder.expires_at) <= now || (reminder.due_at && new Date(reminder.due_at) >= new Date(reminder.expires_at)))
      ? "The private link has expired, or expires before the next reminder is due. Contact the client another way."
      : "";
  const waiting = previousId && reminder?.due_at && new Date(reminder.due_at) > now ? `Under the firm's rule the next reminder is due ${dates.dateTime(reminder.due_at)}.` : "";
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
      <summary>{previousId ? "Approve a reminder" : "Send the approved request by email"}</summary>
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
              sending this private request link from this sender to this exact
              recipient.
            </label>
            {waiting && <p role="status">{waiting}</p>}
            <button disabled={!!waiting}>{previousId ? "Approve and queue this reminder" : "Approve and queue email"}</button>
          </fieldset>
        </form>
      ) : (
        data && (
          <p>
            Email needs a sender (Krith's, or the firm's own under Settings →
            Firm) and a hosted session with sending permission. Manual link
            sharing remains available.
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

// An email whose outcome the app could not know, settled from the provider's own records.
export function SettleEmail({ closeId, emailId }: { closeId: string; emailId: string }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");
  if (done) return <p role="status">Settled as {done}. Refresh to see it in the history.</p>;
  const settle = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const outcome = String(form.get("outcome"));
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/email/${encodeURIComponent(emailId)}/settle`, { expected_state: "unknown", outcome, provider_id: outcome === "accepted" ? String(form.get("provider_id") ?? "").trim() : "", evidence: String(form.get("evidence") ?? "").trim() });
      setDone(outcome);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The settlement could not be saved.");
    } finally {
      setBusy(false);
    }
  };
  return (
    <details>
      <summary>Settle from the provider's records</summary>
      <form onSubmit={(e) => void settle(e)}>
        <fieldset disabled={busy}>
          {error && <p role="alert">{error}</p>}
          <label>Outcome<select name="outcome" defaultValue="accepted"><option value="accepted">Accepted: the provider has this email</option><option value="failed">Failed: the provider has no such email</option></select></label>
          <label>Provider message ID (when accepted)<input name="provider_id" maxLength={200} /></label>
          <label>What the provider's records show and how you searched them<textarea name="evidence" required minLength={5} maxLength={2000} placeholder="Resend logs for the sender, recipient and attempt time show…" /></label>
          <button>Settle</button>
        </fieldset>
      </form>
    </details>
  );
}

export type ReminderStatus = { left: number; due_at?: string; expires_at?: string };
export type EmailRecord = { id: string; state: string; delivery: string; reminder_of?: string; error: string; approved_by?: string; created_at: string };
export function EmailHistory({emails, closeId}: {emails: EmailRecord[]; closeId?: string}) {
  const dates = useDates();
  return <section aria-label="Email delivery history"><h5>Email history</h5>{emails.map(m=><div key={m.id}><p>
    {m.reminder_of ? "Reminder" : "Initial email"} · {dates.dateTime(m.created_at)} · {m.state.replaceAll("_"," ")} · delivery: {m.delivery.replaceAll("_"," ")}{m.approved_by ? ` · approved by ${m.approved_by}` : ""}. {m.error}
  </p>{closeId && m.state === "unknown" && <SettleEmail closeId={closeId} emailId={m.id} />}</div>)}<p>Delivered means accepted by the recipient’s mail server, not read. Reminders require a separate approval after confirmed delivery. Replies and delivery failures block reminders.</p></section>;
}
