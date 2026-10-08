import { Link } from "react-router";

// What keeps client data, and the authority over it, where it belongs. Each statement follows what the service does
// today; when the code changes, change this page (docs/marketing-site.md). About restates Authority, Evidence and
// Separation in brief; change it with them.
const CONTROLS = [
  { key: "Authority", title: "Agents can’t act alone", text: "Agents prepare drafts and questions. They can’t approve an entry, send anything to a client, write to a ledger or sign off a close. People do, with their own permissions." },
  { key: "Evidence", title: "Every figure keeps its source", text: "When an agent takes a figure from a document, the words it relied on must appear exactly once and show the same amount or date. Otherwise the figure becomes a question for a person." },
  { key: "Separation", title: "Each firm is walled off", text: "Row-level security in the database keeps each firm’s data apart, so one firm’s requests cannot read another’s. Firm administrators decide who works on which clients." },
  { key: "Encryption", title: "Connections and credentials", text: "Connections are encrypted in transit. Ledger and email credentials are encrypted before they are stored, and are never shown back." },
  { key: "Uploads", title: "Client files are quarantined", text: "Files clients send stay quarantined until a malware scan clears them and a person at the firm releases them." },
  { key: "AI", title: "Your data trains nothing", text: "Client data is never used to train models, and one firm’s data is never used in another firm’s work. Card, bank account and Social Security numbers and sign-in details are removed before text reaches a model. The one exception is reading a PDF: each page goes whole, as an image, to Google’s Gemini, on paid terms under which Google does not use it to improve its products." },
  { key: "Staff access", title: "Our team sees metadata", text: "By default our staff see states, counts and errors. Seeing client content needs a separate permission and a stated reason, and every access goes into an audit log that cannot be edited." },
  { key: "Hosting", title: "Where the data lives", text: "The database runs on Neon, hosted by Amazon Web Services in the United States (Ohio). The application runs on Cloudflare’s network, and sign-in is handled by WorkOS." },
  { key: "Your control", title: "Your firm holds the keys", text: "Your firm decides how long client data is kept, can place a legal hold, and can export each close before deleting it. The Security center in the workspace shows which services receive your firm’s data." },
];

export function Security() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Security</p>
        <h1>How we protect your clients’ data.</h1>
        <p className="k-lede">Agents prepare the work. People keep every decision. Here is what keeps the data, and the authority, where it belongs.</p>
      </div>
      <div className="k-controls">
        {CONTROLS.map((c) => (
          <article key={c.key}>
            <span className="k-key">{c.key}</span>
            <h2>{c.title}</h2>
            <p>{c.text}</p>
          </article>
        ))}
      </div>
      <div className="k-after">
        <Link to="/privacy">Privacy notice</Link>
        <span>
          A security review to run? <Link to="/contact">Talk to us</Link>
        </span>
      </div>
    </main>
  );
}
