import { Link } from "react-router";
import { JsonLd } from "./schema";

// The questions firms ask before they hand Krith a month, indexed the way a workpaper lists its accounts. Each answer
// says what Security, Privacy and About say, in the same terms, and only what the service does today; change them
// together (docs/marketing-site.md).
type Topic = { id: string; mark: string; name: string; entries: { q: string; a: string }[]; more?: { to: string; label: string } };

const TOPICS: Topic[] = [
  {
    id: "product",
    mark: "PR",
    name: "The product",
    entries: [
      { q: "What is Krith?", a: "A team of AI agents that prepares the work behind an accounting firm’s engagements. The agents do the preparation; your people review it, decide and sign." },
      { q: "Who is it for?", a: "Accounting firms and finance teams that keep and review books, for their clients or for their own company." },
      { q: "What does Krith prepare?", a: "The work that comes before a signature: gathering statements and documents, matching statement lines to the ledger, tracing each difference to its source, drafting entries and schedules, and drafting questions for whatever is missing. The work arrives ready for review, with what needs a decision on top." },
      { q: "Does it replace our accounting software?", a: "No. Your ledger stays the book of record. Krith reads it once a firm member connects it, and writes to it only an entry a person has approved, one at a time. Writing stays off until your firm turns it on." },
      { q: "Which ledgers does it connect to?", a: "QuickBooks Online and Xero, connected by a firm member for each client. If your clients keep their books somewhere else, talk to us." },
    ],
    more: { to: "/about", label: "About Krith" },
  },
  {
    id: "agents",
    mark: "AG",
    name: "Working with the agents",
    entries: [
      { q: "What do the agents do, and what do our people do?", a: "Agents gather, match, trace and draft. People approve entries, send anything to a client, write to a ledger and sign off a close, each with their own permissions. The agents can’t do any of those." },
      { q: "Can an agent post an entry or email a client on its own?", a: "No. An agent can draft an entry or a question, but a person at your firm approves every entry and decides what reaches a client." },
      { q: "How do we know a figure is right?", a: "Every figure keeps its source. When an agent takes a figure from a document, the words it relied on must appear in it exactly once and show the same amount or date. If they don’t, the figure becomes a question for a person instead." },
      { q: "How do we give Krith work?", a: "Hand it a goal in plain words, such as “Hand over my clients due Friday,” and the agents plan the work and keep at it after you close the tab. You can also ask about your clients as you would ask a colleague." },
      { q: "Do our clients need an account?", a: "No. When your firm sends a request, the client replies and uploads files through a private link. Files they send stay quarantined until a malware scan clears them and someone at your firm releases them." },
    ],
    more: { to: "/#how", label: "See the agents at work" },
  },
  {
    id: "data",
    mark: "DA",
    name: "Your data",
    entries: [
      { q: "Where is our data stored?", a: "In the United States. Client data, including files, is kept in a database hosted on Amazon Web Services, and the application runs on Cloudflare’s network." },
      { q: "Is our data used to train AI?", a: "No. Krith doesn’t train models on client data, and an AI provider is turned on only under terms or account settings that exclude training on what it reads." },
      { q: "Can another firm see our data?", a: "No. Each firm’s records are walled off from every other firm’s in the database, and one firm’s data is never used in another firm’s work. Within your firm, administrators decide who works on which clients." },
      { q: "Who at Krith can see our clients’ data?", a: "By default our team sees only states, counts and errors. Seeing client content needs a separate permission and a stated reason, and every access is written to an audit log that can’t be edited." },
      { q: "Can we take our data out, or delete it?", a: "Yes. Your firm decides how long client data is kept and can place a legal hold. Before deleting a close, you can download its documents, workpapers, reviews and sign-off history." },
    ],
    more: { to: "/security", label: "How we protect client data" },
  },
  {
    id: "start",
    mark: "ST",
    name: "Getting started",
    entries: [
      { q: "How do we get started?", a: "Sign in with your company account, and the first person from your firm sets up its workspace; if your firm can’t sign in yet, talk to us. Or talk to us first: a person reads your note, replies and shows you what the agents would prepare and what stays with your people." },
      { q: "What does it cost?", a: "It depends on the size of your firm and the work you hand over. We go through it when we talk." },
    ],
  },
];

const total = TOPICS.reduce((n, t) => n + t.entries.length, 0);
const questions = {
  "@type": "FAQPage",
  mainEntity: TOPICS.flatMap((t) => t.entries.map((e) => ({ "@type": "Question", name: e.q, acceptedAnswer: { "@type": "Answer", text: e.a } }))),
};

export function Faq() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Questions</p>
        <h1>Asked and answered.</h1>
        <p className="k-lede">What firms ask before they hand Krith a month. If yours isn’t here, we’re glad to answer it.</p>
      </div>
      <nav className="k-faq-index" aria-label="Questions by topic">
        <div className="k-lhead">
          <span>Index</span>
          <span>{TOPICS.length} topics</span>
        </div>
        <ol>
          {TOPICS.map((t) => (
            <li key={t.id}>
              <span className="k-mk" aria-hidden="true">
                {t.mark}
              </span>
              <Link to={`#${t.id}`}>{t.name}</Link>
              <span className="k-faq-count">{t.entries.length}</span>
            </li>
          ))}
        </ol>
        <p className="k-faq-total">
          <span>Questions answered</span>
          <span className="k-faq-count">{total}</span>
        </p>
      </nav>
      {TOPICS.map((t) => (
        <section key={t.id} id={t.id} className="k-faq-topic" aria-labelledby={`${t.id}-title`}>
          <h2 id={`${t.id}-title`}>
            <span className="k-mk" aria-hidden="true">
              {t.mark}
            </span>
            {t.name}
          </h2>
          <div>
            {t.entries.map((e) => (
              <details key={e.q} className="k-qa">
                <summary>{e.q}</summary>
                <p>{e.a}</p>
              </details>
            ))}
            {t.more && (
              <p className="k-faq-more">
                <Link to={t.more.to}>
                  {t.more.label} <span aria-hidden="true">→</span>
                </Link>
              </p>
            )}
          </div>
        </section>
      ))}
      <JsonLd data={questions} />
      <div className="k-faq-ask">
        <p>Didn’t find your question?</p>
        <Link className="k-pill" to="/contact">
          Talk to us <span aria-hidden="true">→</span>
        </Link>
      </div>
    </main>
  );
}
