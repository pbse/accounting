import { useState, type FormEvent, type ReactNode } from "react";
import { DialogActions } from "@close/ui/components/Dialog";
import { PasscodeNotice, SendRequestEmail } from "../closes/Email";
import { workspaceRequest } from "../closes/workspace";

// What a request asked of a client returns: the request, its month's close, its wording and its private link.
export type Asked = { id: string; close_id: string; body: string; link_token: string; passcode: string; recipient: string; values: Record<string, string> };

// AskClient asks a client for something through a request (docs/books-sources.md, Client-provided books): its own
// fields, then the recipient. Creating it sends nothing: the private link is shown, and its email is approved here,
// apart, as any request's is; approving it issues a new link, sent in the email, and revokes the one shown before.
export function AskClient({
  path,
  fields,
  values,
  after,
  onClose,
}: {
  path: string;
  fields: ReactNode;
  values: (form: FormData) => Record<string, string>;
  after: (asked: Asked) => ReactNode;
  onClose: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [asked, setAsked] = useState<Asked>();
  const [queued, setQueued] = useState<{ passcode: string }>();
  async function ask(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const own = values(form), recipient = String(form.get("recipient"));
    setBusy(true);
    setError("");
    try {
      const saved = await workspaceRequest(path, { ...own, recipient, confirmed: form.get("confirmed") === "on" });
      setAsked({ ...saved, recipient, values: own });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The request could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  if (asked) {
    return (
      <div className="stack">
        <pre>{asked.body}</pre>
        {queued ? (
          <>
            <p role="status">The email to {asked.recipient} is queued with a new private link; the link shown before no longer works.</p>
            <PasscodeNotice passcode={queued.passcode} />
          </>
        ) : (
          <>
            <label>
              Private link
              <input readOnly value={`${window.location.origin}/request#${asked.link_token}`} onFocus={(e) => e.currentTarget.select()} />
            </label>
            <PasscodeNotice passcode={asked.passcode} />
            <SendRequestEmail closeId={asked.close_id} requestId={asked.id} recipient={asked.recipient} onQueued={(passcode) => setQueued({ passcode })} />
          </>
        )}
        {after(asked)}
        <DialogActions>
          <button type="button" onClick={onClose}>
            Done
          </button>
        </DialogActions>
      </div>
    );
  }
  return (
    <form onSubmit={ask}>
      <fieldset disabled={busy}>
        {fields}
        <label>
          Client recipient
          <input type="email" name="recipient" required maxLength={254} />
        </label>
        <label className="checkbox-label">
          <input type="checkbox" name="confirmed" required /> I reviewed the request and the recipient
        </label>
        <p className="note">Creating the request does not send an email: that is approved next. Anyone with the link can use it unless the firm requires a passcode.</p>
        {error && <p role="alert">{error}</p>}
        <DialogActions>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
          <button className="primary">{busy ? "Asking…" : "Create the request"}</button>
        </DialogActions>
      </fieldset>
    </form>
  );
}
