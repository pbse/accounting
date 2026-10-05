import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { apiBaseUrl } from "../../lib/api";

// Where "Talk to us" leads. The API keeps each note and forwards it to our inbox (docs/marketing-site.md); mail to
// hello@getkrith.com reaches the same inbox.
export const MAILBOX = "hello@getkrith.com";

export function Contact() {
  const [state, setState] = useState<"idle" | "sending" | "sent" | "failed">("idle");
  const [sender, setSender] = useState({ name: "", email: "" });
  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    if (!form.reportValidity()) return;
    const fields = Object.fromEntries(new FormData(form)) as Record<string, string>;
    setState("sending");
    try {
      const response = await fetch(`${apiBaseUrl}/v1/contact`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(fields),
      });
      if (!response.ok) throw new Error(`contact ${response.status}`);
      setSender({ name: fields.name.trim().split(/\s+/)[0], email: fields.email.trim() });
      setState("sent");
    } catch {
      setState("failed");
    }
  }
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Contact</p>
        <h1>Talk to us.</h1>
        <p className="k-lede">Tell us how your firm works and where month-end takes its time. A person reads every note and replies.</p>
      </div>
      <div className="k-contact">
        <div>
          {state === "sent" ? (
            <div className="k-sent" role="status">
              <p>
                Thank you, {sender.name}. Your note is with us, and a person will reply to {sender.email}.
              </p>
            </div>
          ) : (
            <form className="k-form" onSubmit={send} noValidate>
              <div className="k-pair">
                <p className="k-field">
                  <label htmlFor="contact-name">Name</label>
                  <input id="contact-name" name="name" autoComplete="name" maxLength={120} required />
                </p>
                <p className="k-field">
                  <label htmlFor="contact-email">Work email</label>
                  <input id="contact-email" name="email" type="email" autoComplete="email" maxLength={254} required />
                </p>
              </div>
              <p className="k-field">
                <label htmlFor="contact-firm">Firm or company</label>
                <input id="contact-firm" name="firm" autoComplete="organization" maxLength={160} required />
              </p>
              <p className="k-field">
                <label htmlFor="contact-note">
                  What should we know? <small>Optional</small>
                </label>
                <textarea id="contact-note" name="note" rows={4} maxLength={4000} placeholder="For example: 40 clients on QuickBooks Online, and most closes take a week." />
              </p>
              {/* Left empty by people; a form filled in by a script usually completes it. */}
              <p className="k-trap" aria-hidden="true">
                <label htmlFor="contact-website">Website</label>
                <input id="contact-website" name="website" tabIndex={-1} autoComplete="off" />
              </p>
              {state === "failed" && (
                <p className="k-alert" role="alert">
                  Your note didn’t send. Please try again, or write to <a href={`mailto:${MAILBOX}`}>{MAILBOX}</a>.
                </p>
              )}
              <div className="k-send">
                <button className="k-pill k-pill-lg" type="submit" disabled={state === "sending"}>
                  {state === "sending" ? "Sending…" : "Send"} <span aria-hidden="true">→</span>
                </button>
                <p className="k-fine">
                  We use what you send only to reply to you. <Link to="/privacy">Privacy notice</Link>
                </p>
              </div>
            </form>
          )}
        </div>
        <aside className="k-next" aria-labelledby="next-title">
          <h2 id="next-title">What happens next</h2>
          <ol>
            <li>
              <span>
                <b>A person reads your note</b> and replies.
              </span>
            </li>
            <li>
              <span>
                <b>We talk through</b> how your firm prepares and reviews its closes.
              </span>
            </li>
            <li>
              <span>
                <b>We show you the work, prepared:</b> what the agents do, and what stays with your people.
              </span>
            </li>
          </ol>
          <p className="k-mailbox">
            Prefer email?
            <br />
            <a href={`mailto:${MAILBOX}`}>{MAILBOX}</a>
          </p>
        </aside>
      </div>
    </main>
  );
}
