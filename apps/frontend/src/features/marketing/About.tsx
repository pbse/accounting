import { Link } from "react-router";

// Krith first: why it exists, the commitments the Security page states in full, how a firm works with it and where
// it starts. It stays high level: what the product does today, and what comes next stated as intent. The company file comes last, with when
// and where it was founded, and is repeated as structured data so search engines and anyone verifying the company
// can read it (docs/marketing-site.md).
const ORGANIZATION = {
  "@context": "https://schema.org",
  "@type": "Organization",
  name: "Krith",
  url: "https://getkrith.com",
  description: "Krith is a team of AI agents that prepares accounting work for firms, starting with the month-end close. People review, decide and sign.",
  foundingDate: "2026",
  foundingLocation: { "@type": "Place", name: "New York, NY" },
  address: { "@type": "PostalAddress", addressLocality: "New York", addressRegion: "NY", addressCountry: "US" },
};

// Each follows a control on the Security page; change both together.
const PRINCIPLES = [
  { key: "People decide", title: "Agents prepare. People sign.", text: "Agents draft entries and questions. They can’t approve an entry, send anything to a client, write to a ledger or sign off a close." },
  { key: "Evidence", title: "Every figure keeps its source", text: "A figure taken from a document must match the words it came from. If it doesn’t, it becomes a question for a person." },
  { key: "Your data", title: "Each firm’s data stays its own", text: "Each firm is walled off in the database, and client data never trains models." },
];

// How a firm works with the agents, in brief: each holds for the product today.
const WORKING = [
  { key: "01 · Goal", title: "Hand it a goal", text: "“Get these closes ready for review by Friday.” Agents plan the work and keep at it after you close the tab." },
  { key: "02 · Ask", title: "Ask as you would a colleague", text: "Ask about your clients in plain words. Answers point to the saved work behind them." },
  { key: "03 · Decide", title: "Start with the decisions", text: "Your day opens on what needs a person, not on a list of tasks. Agents draft the questions for clients; your team chooses what goes out." },
];

export function About() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">About Krith</p>
        <h1>Krith means done.</h1>
        <p className="k-lede">
          From the Sanskrit <i>kṛta</i>: done, made, accomplished. Krith prepares the work behind every engagement, starting with the month-end close, so your people can review, decide and sign.
        </p>
      </div>
      <section className="k-why" aria-labelledby="why-title">
        <h2 id="why-title">Why Krith</h2>
        <div>
          <p>Much of a month-end close is preparation: gathering statements, matching every line, tracing each difference and drafting the entries. The judgement that matters comes at the end, and it belongs to the people who sign.</p>
          <p>Krith gives that preparation to a team of agents and keeps every decision with your firm. Each figure carries its source, so whoever signs can see why it is right.</p>
        </div>
      </section>
      <div className="k-controls">
        {PRINCIPLES.map((p) => (
          <article key={p.key}>
            <span className="k-key">{p.key}</span>
            <h2>{p.title}</h2>
            <p>{p.text}</p>
          </article>
        ))}
      </div>
      <div className="k-after">
        <Link to="/security">How we protect client data</Link>
        <span>
          Questions? <Link to="/contact">Talk to us</Link>
        </span>
      </div>
      <section aria-labelledby="working-title">
        <div className="k-why">
          <h2 id="working-title">Working with Krith</h2>
          <div>
            <p>Krith isn’t software your team operates. It is a team of agents that takes on the preparation, shows its working and stops where a signature begins.</p>
          </div>
        </div>
        <div className="k-controls">
          {WORKING.map((w) => (
            <article key={w.key}>
              <span className="k-key">{w.key}</span>
              <h3>{w.title}</h3>
              <p>{w.text}</p>
            </article>
          ))}
        </div>
      </section>
      <section className="k-why" aria-labelledby="next-title">
        <h2 id="next-title">Starting with the close</h2>
        <div>
          <p>The month-end close is the work every engagement leans on. It is where much of the preparation sits, and where a missed detail travels furthest. So that is where Krith begins.</p>
          <p>Next, we mean to take on the books between closes, the handoff at year end and the cash decisions that follow. Each joins only when the work before it holds up, under the same rules: agents prepare, every figure keeps its source, and a person decides.</p>
        </div>
      </section>
      <div className="k-after">
        <span>
          See what Krith would prepare for one of your clients. <Link to="/contact">Talk to us</Link>
        </span>
      </div>
      <figure className="k-file" aria-labelledby="file-title">
        <div className="k-lhead">
          <span id="file-title">Company file</span>
          <span>Krith · New York</span>
        </div>
        <dl>
          <div className="k-file-row">
            <dt>For</dt>
            <dd>Accounting firms and finance teams</dd>
          </div>
          <div className="k-file-row">
            <dt>Founded</dt>
            <dd>2026, in New York</dd>
          </div>
          <div className="k-file-row">
            <dt>Headquarters</dt>
            <dd>New York City</dd>
          </div>
        </dl>
      </figure>
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(ORGANIZATION) }} />
    </main>
  );
}
