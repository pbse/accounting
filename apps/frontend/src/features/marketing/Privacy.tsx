import { Link } from "react-router";
import { MAILBOX } from "./Contact";

// The privacy notice. Each statement follows what the service does today; when the code changes, change the page and
// its date (docs/marketing-site.md).
const updated = "12 October 2026";

export function Privacy() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Trust</p>
        <h1>Privacy notice</h1>
        <p className="k-lede">How Krith handles information on this website and in the Krith workspace, where accounting firms prepare their clients’ work.</p>
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
              <strong>On this website:</strong> what your browser sends to load a page, and a check of whether you are signed in to the workspace, which contacts our sign-in service and sends any sign-in cookie you have. We use no analytics, advertising or tracking, and load no outside fonts or scripts. Our hosting provider, Cloudflare, processes each request, including your IP address, to deliver the site and protect it from abuse.
            </li>
            <li>
              <strong>People who write to us:</strong> the name, email address, firm and note you send through our contact page or to {MAILBOX}. We keep it to reply to you, and forward it to our team’s inbox. To limit repeated notes, we also keep a one-way digest of the network address a note came from, never the address itself, and clear it after an hour.
            </li>
            <li>
              <strong>Firm members:</strong> name, email address and roles, which our sign-in provider holds for us; which clients each person may work on; and a record of what each person does in Krith, such as confirming facts, approving drafts and signing off a close. When a firm invites someone, we email them the invitation from Krith. When someone sets up or joins a firm by signing in, we email them once, from Krith. We keep a record of each of these emails for 30 days.
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
            <li>The technology providers that run Krith for us, described below, only to provide the service.</li>
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
            Krith’s database is hosted in the United States. Our technology providers may process information in the United States and other countries where they operate.
          </p>
        </section>
        <section>
          <h2>The technology behind Krith</h2>
          <p>
            Krith is built on established cloud and AI providers. Among them, Cloudflare and Amazon Web Services host the website, the application and its database, and AI models from OpenAI, Anthropic and Google read documents and help prepare the work. Others handle sign-in and send the emails a firm approves. Each processes data to provide Krith’s service and to keep its own service secure, for example by briefly holding requests to detect abuse, and we don’t allow any of them to use firm or client data to train AI models.
          </p>
          <p>
            Firm and client data is scoped to one firm. Each firm’s records are walled off from every other firm’s in our database, the work Krith does for a firm reads only that firm’s data, and one firm’s data is never used in another firm’s work.
          </p>
          <p>
            QuickBooks, Xero, Stripe and Google Drive are the firm’s own accounts. Krith reads from them only after a firm member connects them, and writes to a ledger only under a grant the firm approves, one approved entry at a time. A firm can ask us for the current list of providers that process its data, and we tell the firms that use Krith before adding one.
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
            We set no advertising or analytics cookies. Signing in uses cookies from our sign-in provider or from Krith itself to keep you signed in; they are not used for anything else. The workspace also stores, in your browser, the page to return to after signing in and whether you left the assistant panel open.
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
