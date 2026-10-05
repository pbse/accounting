import { Link } from "react-router";
import { MAILBOX } from "./Contact";

// The privacy notice. Each statement follows what the service does today; when the code changes, change the page and
// its date (docs/marketing-site.md).
const updated = "5 October 2026";

const PROVIDERS = [
  { name: "Cloudflare", does: "Hosts the website and runs the application; scans client files for malware; receives email sent to our addresses and delivers contact notes to our inbox." },
  { name: "Neon, on Amazon Web Services", does: "The database, in the United States (Ohio)." },
  { name: "WorkOS", does: "Sign-in, roles and invitations." },
  { name: "OpenAI or Anthropic", does: "AI reading of documents. Your firm’s workspace names the provider it uses." },
  { name: "Resend", does: "Sends emails a person at the firm has approved, through the firm’s own sender." },
  { name: "Google (Gmail)", does: "Our team’s inbox, which receives notes sent through our contact page or to our email address." },
];

export function Privacy() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Trust</p>
        <h1>Privacy notice</h1>
        <p className="k-lede">How Krith handles information on this website and in the Krith workspace, where accounting firms prepare their clients’ closes.</p>
      </div>
      <article className="k-doc">
        <p className="k-upd">Last updated {updated}</p>
        <section>
          <h2>Who decides what</h2>
          <p>
            Krith is an accounting preparation workspace for accounting firms. A firm decides which of its clients’ documents and ledgers to bring into Krith and why; for that client data, the firm is responsible and we process it on the firm’s behalf, only to provide the service. For the accounts of the firm’s own people, for visitors to this website and for people who write to us, we are responsible.
          </p>
        </section>
        <section>
          <h2>What we collect</h2>
          <ul>
            <li>
              <strong>On this website:</strong> what your browser sends to load a page, and a check of whether you are signed in to the workspace, which contacts our sign-in service (WorkOS, or Krith itself) and sends any sign-in cookie you have. We use no analytics, advertising or tracking, and load no outside fonts or scripts. Our hosting provider, Cloudflare, processes each request, including your IP address, to deliver the site and protect it from abuse.
            </li>
            <li>
              <strong>People who write to us:</strong> the name, email address, firm and note you send through our contact page or to {MAILBOX}. We keep it to reply to you, and forward it to our team’s inbox. To limit repeated notes, we also keep a one-way digest of the network address a note came from, never the address itself, and clear it after an hour.
            </li>
            <li>
              <strong>Firm members:</strong> name, email address and roles, which our sign-in provider WorkOS holds for us; which clients each person may work on; and a record of what each person does in Krith, such as confirming facts, approving drafts and signing off a close.
            </li>
            <li>
              <strong>Client data a firm provides:</strong> documents the firm uploads, forwards or imports from Google Drive; replies and files a client sends through a private request link; ledger data read from QuickBooks or Xero and payout reports from Stripe, when the firm connects them; and the workpapers, questions, reviews and assistant conversations built from them.
            </li>
            <li>
              <strong>Client contacts:</strong> the email address a firm records for each client request it approves, whether Krith emails the request or the firm shares its private link itself, and the email addresses of the client’s customers that the client provided for payment reminders.
            </li>
          </ul>
        </section>
        <section>
          <h2>How we use it</h2>
          <p>
            To run the service: reading documents, preparing drafts for the firm’s review, checking them against the ledger and keeping their history. We also use it to keep the service secure and working; our operational alerts carry only IDs, states and ages, never client names or documents. We don’t sell information, we don’t use it for advertising, and we don’t use firm or client data to train AI models. <Link to="/security">Security</Link> explains how our agents use AI.
          </p>
        </section>
        <section>
          <h2>Who else sees it</h2>
          <ul>
            <li>The service providers listed under “Who processes data for us”, each for its stated purpose.</li>
            <li>The ledgers, Google Drive and email sender a firm connects, which receive only what the firm authorizes.</li>
            <li>
              Our own staff see operational metadata (states, counts, errors and IDs) by default. Seeing client content requires a separate permission and a stated reason, and every such access is written to an audit log that cannot be edited.
            </li>
            <li>Anyone the law requires us to disclose information to.</li>
          </ul>
          <p>A person, not the software, approves anything sent to a client or written to a ledger.</p>
        </section>
        <section>
          <h2>Where it is stored</h2>
          <p>
            Our database runs on Neon, hosted by Amazon Web Services in the United States (Ohio). The application runs on Cloudflare’s network. Service providers may process information in the United States and other countries where they operate.
          </p>
        </section>
        <section>
          <h2>Who processes data for us</h2>
          <div className="k-tablewrap" role="region" aria-label="Service providers" tabIndex={0}>
            <table>
              <thead>
                <tr>
                  <th scope="col">Provider</th>
                  <th scope="col">What it does for Krith</th>
                </tr>
              </thead>
              <tbody>
                {PROVIDERS.map((p) => (
                  <tr key={p.name}>
                    <th scope="row">{p.name}</th>
                    <td>{p.does}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p>
            QuickBooks, Xero, Stripe and Google Drive are the firm’s own accounts. Krith reads from them only after a firm member connects them, and writes to a ledger only under a grant the firm approves, one approved entry at a time. We update this list before adding a provider, and tell the firms that use Krith first.
          </p>
        </section>
        <section>
          <h2>How we protect it</h2>
          <p>
            Connections are encrypted in transit, each firm’s data is separated by row-level security, credentials are encrypted before they are stored, and client files stay quarantined until a malware scan clears them. <Link to="/security">Security</Link> has the details.
          </p>
        </section>
        <section>
          <h2>Cookies and browser storage</h2>
          <p>
            We set no advertising or analytics cookies. Signing in uses cookies from WorkOS or from Krith itself to keep you signed in; they are not used for anything else. The workspace also stores, in your browser, the page to return to after signing in and whether you left the assistant panel open.
          </p>
        </section>
        <section>
          <h2>How long we keep it</h2>
          <p>
            A firm decides how long its client data is kept: an administrator sets each close’s retention date and can place a legal hold. A few working records, such as assistant conversations, are trimmed automatically.
          </p>
          <p>
            Before deleting a close, a firm can download its documents, workpapers, reviews and sign-off history. Deleting is recorded and leaves only a minimal receipt of which month was deleted, by whom and why. If a firm leaves, we suspend its workspace without deleting anything, and remaining data is deleted as agreed in writing.
          </p>
        </section>
        <section>
          <h2>Your choices and rights</h2>
          <p>
            If you are a client of an accounting firm, ask your firm first: it decides what is held about you in Krith, and we will help it answer. Firm members, website visitors and people who write to us can ask us to access, correct or delete their information, or object to how we use it, subject to the law that applies to you.
          </p>
          <p>
            Questions about this notice, or a privacy request? Write to <a href={`mailto:${MAILBOX}`}>{MAILBOX}</a> or use our <Link to="/contact">contact page</Link>.
          </p>
        </section>
        <section>
          <h2>Changes</h2>
          <p>We will update this page when our practices change and change the date above. We tell the firms that use Krith about material changes before they take effect.</p>
        </section>
      </article>
    </main>
  );
}
