import type { ReactNode } from "react";
import { Link } from "react-router";

// The public legal and data pages. Each statement follows what the service's code does today; when the code changes,
// change the page and its date. The workspace's Security center shows which of these services a firm is using.
const updated = "28 September 2026";

export const legalPages = [
  { to: "/privacy", label: "Privacy" },
  { to: "/subprocessors", label: "Subprocessors" },
  { to: "/ai", label: "AI and your data" },
  { to: "/data-retention", label: "Retention and deletion" },
  { to: "/pilot-terms", label: "Pilot terms" },
];

function LegalPage({ eyebrow, title, intro, children }: { eyebrow: string; title: string; intro: ReactNode; children: ReactNode }) {
  return (
    <main id="main" className="public-page compact-page legal">
      <p className="eyebrow">{eyebrow}</p>
      <h1>{title}</h1>
      <p className="hero-copy">{intro}</p>
      <p className="legal-updated">Last updated {updated}</p>
      {children}
    </main>
  );
}

// Contact is the same on every page until a public address exists.
function Contact() {
  return (
    <p>
      We don’t have a public contact address yet; it will appear here and on our <Link to="/contact">contact page</Link> when it does. A firm in a pilot can raise any request, including a privacy request, with its named pilot contact.
    </p>
  );
}

export function Privacy() {
  return (
    <LegalPage eyebrow="PRIVACY" title="Privacy notice" intro="How Close handles information on this website and in the Close workspace, where accounting firms prepare their clients’ closes.">
      <section>
        <h2>Who decides what</h2>
        <p>
          Close is an accounting preparation workspace for accounting firms. A firm decides which of its clients’ documents and ledgers to bring into Close and why; for that client data, the firm is responsible and we process it on the firm’s behalf, only to provide the service. For the accounts of the firm’s own people, and for visitors to this website, we are responsible.
        </p>
      </section>
      <section>
        <h2>What we collect</h2>
        <ul>
          <li>
            <strong>On this website:</strong> nothing beyond what your browser sends to load a page. We use no analytics, advertising or tracking, and load no outside fonts or scripts. Our hosting provider, Cloudflare, processes each request, including your IP address, to deliver the site and protect it from abuse.
          </li>
          <li>
            <strong>Firm members:</strong> name, email address and roles, which our sign-in provider WorkOS holds for us; which clients each person may work on; and a record of what each person does in Close, such as confirming facts, approving drafts and signing off a close.
          </li>
          <li>
            <strong>Client data a firm provides:</strong> documents the firm uploads, forwards or imports from Google Drive; replies and files a client sends through a private request link; ledger data read from QuickBooks or Xero and payout reports from Stripe, when the firm connects them; and the workpapers, questions, reviews and assistant conversations built from them.
          </li>
          <li>
            <strong>Client contacts:</strong> the email address a firm approves a client request to, when the firm sends requests by email.
          </li>
        </ul>
      </section>
      <section>
        <h2>How we use it</h2>
        <p>
          To run the service: reading documents, preparing drafts for the firm’s review, checking them against the ledger and keeping their history. We also use it to keep the service secure and working; our operational alerts carry only IDs, states and ages, never client names or documents. We don’t sell information, we don’t use it for advertising, and we don’t use firm or client data to train AI models. <Link to="/ai">AI and your data</Link> explains our use of AI.
        </p>
      </section>
      <section>
        <h2>Who else sees it</h2>
        <ul>
          <li>
            The service providers on our <Link to="/subprocessors">subprocessors</Link> page, each for its stated purpose.
          </li>
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
        <h2>How we protect it</h2>
        <ul>
          <li>Connections are encrypted in transit.</li>
          <li>Each firm’s data is separated in the database by row-level security, so one firm’s requests cannot read another’s.</li>
          <li>Ledger and email credentials are encrypted before they are stored, and are never shown back.</li>
          <li>Files clients send stay quarantined until a malware scan clears them and a person at the firm releases them.</li>
          <li>Card, IBAN and Social Security numbers, labelled bank account numbers and sign-in details are removed from document text, policies and assistant questions before they are sent to an AI model.</li>
          <li>Sign-in is handled by WorkOS; firm administrators decide who may work on which clients.</li>
        </ul>
      </section>
      <section>
        <h2>Cookies and browser storage</h2>
        <p>
          We set no advertising or analytics cookies. Signing in uses cookies from WorkOS or from Close itself to keep you signed in; they are not used for anything else. The workspace also stores, in your browser, the page to return to after signing in and whether you left the assistant panel open.
        </p>
      </section>
      <section>
        <h2>How long we keep it</h2>
        <p>
          A firm decides when its client data is deleted; nothing is deleted automatically. <Link to="/data-retention">Retention and deletion</Link> has the details.
        </p>
      </section>
      <section>
        <h2>Your choices and rights</h2>
        <p>
          If you are a client of an accounting firm, ask your firm first: it decides what is held about you in Close, and we will help it answer. Firm members and website visitors can ask us to access, correct or delete their information, or object to how we use it, subject to the law that applies to you.
        </p>
        <Contact />
      </section>
      <section>
        <h2>Changes</h2>
        <p>We will update this page when our practices change and change the date above. We tell firms in a pilot about material changes before they take effect.</p>
      </section>
    </LegalPage>
  );
}

const providers: { name: string; purpose: string; receives: string }[] = [
  { name: "Cloudflare", purpose: "Hosts the website and runs the application; scans client files for malware; can receive email forwarded to Close.", receives: "Every request to the site and the application, including uploaded files and the data they carry." },
  { name: "Neon (on Amazon Web Services, United States)", purpose: "The database.", receives: "Everything Close stores, including original documents." },
  { name: "WorkOS", purpose: "Sign-in, roles and invitations; holds each person’s Google Drive permission.", receives: "Names, email addresses and roles of firm members; files a person chooses to import from Google Drive pass through it." },
  { name: "OpenAI", purpose: "AI reading of documents and, if a firm turns them on, written answers.", receives: "What AI and your data describes: document and statement text, policies and assistant questions with sensitive numbers removed, and file names and Work requests as written. Each request asks OpenAI not to store it." },
  { name: "Resend", purpose: "Sends client request emails a person has approved, from the firm’s own sender. Not yet in use.", receives: "The recipient’s email address, a standard notice from the firm’s sender and the private reply link; never the request’s questions or the client’s figures." },
];

export function Subprocessors() {
  return (
    <LegalPage eyebrow="DATA" title="Subprocessors" intro="The service providers that process firm and client data for Close, what each does, and what it receives.">
      <section>
        <div className="legal-table" role="region" aria-label="Subprocessors" tabIndex={0}>
          <table>
            <thead>
              <tr>
                <th scope="col">Provider</th>
                <th scope="col">What it does for Close</th>
                <th scope="col">What it receives</th>
              </tr>
            </thead>
            <tbody>
              {providers.map((p) => (
                <tr key={p.name}>
                  <th scope="row">{p.name}</th>
                  <td>{p.purpose}</td>
                  <td>{p.receives}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p>Our own malware scanner for text and spreadsheet files runs on Cloudflare; if we route operational alerts to an alerting service, it receives only IDs, states and ages of stuck work, never client names or documents.</p>
      </section>
      <section>
        <h2>Services a firm connects</h2>
        <p>
          QuickBooks (Intuit), Xero, Stripe and Google Drive are the firm’s own accounts. Close reads from them only after a firm member connects them, and writes to a ledger only under a grant the firm approves, one approved entry at a time.
        </p>
      </section>
      <section>
        <h2>What is in use for your firm</h2>
        <p>
          The <strong>Security center</strong> in the workspace lists each of these services, exactly what it receives and whether your firm is using it. We will update this page before adding a subprocessor, and tell firms in a pilot first.
        </p>
        <Contact />
      </section>
    </LegalPage>
  );
}

export function AiData() {
  return (
    <LegalPage eyebrow="DATA" title="AI and your data" intro="Close uses AI to read documents and prepare work for your review. Here is what it sends, what it can and cannot do, and what stays with your firm.">
      <section>
        <h2>What the AI does</h2>
        <ul>
          <li>Reads a close’s documents and proposes the facts a workpaper needs, each with the quote it came from.</li>
          <li>In a close with many documents, first chooses which to read from their names, sizes and short excerpts.</li>
          <li>Proposes rows from a bank statement when a person asks.</li>
          <li>Drafts a firm procedure from a policy the firm provides, or from its reviewers’ edits.</li>
          <li>Understands a plain-language request in Work, such as “what’s stuck for this client?”.</li>
          <li>Only if a firm administrator turns them on: writes answers to questions about a close, and investigates a close by reading its records through read-only tools.</li>
          <li>Only for a firm with written answers on: helps our staff draft a workflow plan for one of its closes.</li>
        </ul>
      </section>
      <section>
        <h2>What it cannot do</h2>
        <p>
          It cannot approve a draft, send anything to a client, write to a ledger or sign off a close; people do those, with their own permissions. When it proposes facts from a document, each quote it cites must appear exactly once in the document, and for amounts and dates the quote must show the same value, or the fact becomes a question for a person. Proposed statement rows must quote the statement, with the row’s amount and date in the quote, and written answers must cite text from the records they read. Its answers are labelled as written by the model.
        </p>
      </section>
      <section>
        <h2>What it receives</h2>
        <ul>
          <li>The extracted text of the documents it reads, with the client’s name, the month and the currency.</li>
          <li>For choosing documents: their names, roles, sizes and a short excerpt of each.</li>
          <li>Statement text for a statement a person asks about, and policy text or reviewer edits for a procedure draft.</li>
          <li>A plain-language request in Work, with the client names visible to the person asking.</li>
          <li>With written answers on: the question and the close’s records the answer reads, such as its sign-off blockers, tie-out, changes and exceptions.</li>
          <li>When our staff draft a workflow plan: their instruction, with the client’s name, the month, whether the close is signed off and counts of its preparation work.</li>
        </ul>
        <p>Before document and statement text, policies, reviewer edits and assistant questions and reads are sent, Close removes card numbers, IBANs, Social Security numbers, labelled bank account numbers and passwords or other sign-in details. File names and a plain-language request in Work are sent as written, so keep such details out of them.</p>
      </section>
      <section>
        <h2>Our AI provider</h2>
        <p>
          Close uses OpenAI’s API. OpenAI says data sent to its API is not used to train its models unless the customer opts in; we have not. Each request also asks OpenAI not to store it. OpenAI still keeps abuse-monitoring logs for up to 30 days unless the law requires longer; zero data retention needs OpenAI’s separate approval, which we do not have.
        </p>
        <p>We don’t use your firm’s or your clients’ data to train any model, and one firm’s data is never used in another firm’s work.</p>
      </section>
      <section>
        <h2>Your firm’s controls</h2>
        <ul>
          <li>Written answers, firm-wide answers and investigations are off until a firm administrator turns them on, and each change is recorded with its reason.</li>
          <li>AI document reading is set for the whole service, not per firm. Without it, Close reads only exactly labelled facts, with no AI.</li>
          <li>Each firm has a daily limit on AI calls.</li>
          <li>The workspace’s Security center shows what the AI receives and how many calls your firm made in the last 30 days.</li>
        </ul>
        <Contact />
      </section>
    </LegalPage>
  );
}

export function Retention() {
  return (
    <LegalPage eyebrow="DATA" title="Retention and deletion" intro="Your firm decides how long its client data stays in Close. Nothing is deleted automatically, and deletion is deliberate and recorded.">
      <section>
        <h2>How long data is kept</h2>
        <p>
          A firm keeps each client month (a close) until it deletes it. A firm data administrator records each close’s retention date and can place a legal hold. The exceptions are the assistant: firm-wide conversations are deleted after 30 days, a close’s conversation keeps its latest 200 messages, and removing a person from the firm deletes their conversations.
        </p>
      </section>
      <section>
        <h2>Deleting a close</h2>
        <p>
          A firm data administrator deletes a close once its retention date has arrived, confirming the exact client and month and recording why: the retention period ended, the client asked, or the firm’s contract with the client ended. To act on a client’s request sooner, the administrator first sets the retention date to that day, with the reason. Deletion also waits while anything is still in flight, such as a document being read, an open request link, an email or ledger entry with an uncertain outcome, or a connected ledger. One step then removes the close’s documents, drafts, requests, replies and history. Deleting at the client’s request or at the end of a contract also stops that client’s monthly hand-over and forgets the other names its documents used.
        </p>
      </section>
      <section>
        <h2>What remains after deletion</h2>
        <ul>
          <li>A minimal receipt: the close ID, the month, who deleted it, why and when, and a one-way fingerprint instead of the client’s name.</li>
          <li>Firm-level settings built from the close, such as an approved procedure, with its source removed.</li>
          <li>Our staff audit log, which records staff access and actions.</li>
          <li>Database backups, until they expire: our database provider’s restore history currently covers six hours.</li>
          <li>Copies outside Close: exported binders, emails already delivered, and anything in the firm’s ledger or other connected accounts.</li>
        </ul>
      </section>
      <section>
        <h2>Taking your data with you</h2>
        <p>Before deleting, a firm can download each close’s review package or signed binder, with the original documents, workpapers, calculations and full history.</p>
      </section>
      <section>
        <h2>When a firm leaves</h2>
        <p>
          We can suspend a firm’s workspace, which stops all access and all background work but deletes nothing. The firm exports what it needs and deletes its closes before access ends; how any remaining data is deleted is agreed in writing with each firm in its pilot agreement.
        </p>
        <Contact />
      </section>
    </LegalPage>
  );
}

export function PilotTerms() {
  return (
    <LegalPage eyebrow="PILOTS" title="Pilot terms" intro="Close is in development and runs supervised pilots with a small number of accounting firms. This page summarises how a pilot works; the written agreement we sign with each firm governs, and where they differ, the agreement applies.">
      <section>
        <h2>Before a pilot starts</h2>
        <ul>
          <li>We sign a short written pilot agreement with the firm, including any fees, which are agreed in writing first.</li>
          <li>The firm confirms it may share the client data it brings into Close, and chooses which clients and months take part.</li>
          <li>We tell the firm which connections and features are live for its pilot, and which are demonstrations or not yet active.</li>
          <li>Each side names a contact.</li>
        </ul>
      </section>
      <section>
        <h2>How a pilot runs</h2>
        <ul>
          <li>Close runs beside the firm’s usual close, reading ledgers without changing them.</li>
          <li>Close prepares drafts and questions; the firm’s accountants review them and make every accounting decision.</li>
          <li>Nothing is sent to a client or written to a ledger without a person’s approval. Writing to a live ledger needs a separate written decision and the client’s authority.</li>
          <li>Everything Close prepares is a draft for the firm’s review. It is not an audit, assurance, or tax or legal advice, and the firm remains responsible for its work and for anything it sends or posts.</li>
          <li>We ask the firm to record corrections and time spent, so both sides can judge the results honestly.</li>
        </ul>
      </section>
      <section>
        <h2>Data and confidentiality</h2>
        <p>
          We keep firm and client information confidential and use it only to provide the service, as described in our <Link to="/privacy">privacy notice</Link>, <Link to="/subprocessors">subprocessors</Link>, <Link to="/ai">AI and your data</Link> and <Link to="/data-retention">retention and deletion</Link> pages.
        </p>
      </section>
      <section>
        <h2>Support</h2>
        <p>During a pilot, the firm’s named contact reaches ours directly; response times are set in the pilot agreement.</p>
        <Contact />
      </section>
      <section>
        <h2>Ending a pilot</h2>
        <p>
          Either side can end a pilot as the agreement allows. The firm exports what it needs; its data is then deleted as described in <Link to="/data-retention">retention and deletion</Link> and its agreement.
        </p>
      </section>
    </LegalPage>
  );
}
