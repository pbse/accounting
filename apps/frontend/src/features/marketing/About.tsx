import { Link } from "react-router";

// Krith first: why it exists and the commitments the Security page states in full. The company file comes last,
// with when and where it was founded and who leads it, and is repeated as structured data so search engines and
// anyone verifying the company can read it (docs/marketing-site.md).
const FOUNDER_SITE = "https://prashantb.com";

const ORGANIZATION = {
  "@context": "https://schema.org",
  "@type": "Organization",
  name: "Krith",
  url: "https://getkrith.com",
  foundingDate: "2026",
  foundingLocation: { "@type": "Place", name: "New York, NY" },
  address: { "@type": "PostalAddress", addressLocality: "New York", addressRegion: "NY", addressCountry: "US" },
  founder: { "@type": "Person", name: "Prashant Bansal", jobTitle: "Founder and CEO", url: FOUNDER_SITE },
};

// Each follows a control on the Security page; change both together.
const PRINCIPLES = [
  { key: "People decide", title: "Agents prepare. People sign.", text: "Agents draft entries and questions. They can’t approve an entry, send anything to a client, write to a ledger or sign off a close." },
  { key: "Evidence", title: "Every figure keeps its source", text: "A figure taken from a document must match the words it came from. If it doesn’t, it becomes a question for a person." },
  { key: "Your data", title: "Each firm’s data stays its own", text: "Each firm is walled off in the database, and client data never trains models." },
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
          <div className="k-file-row">
            <dt>Leadership</dt>
            <dd>
              <a href={FOUNDER_SITE}>Prashant Bansal</a>, Founder and CEO
            </dd>
          </div>
        </dl>
      </figure>
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(ORGANIZATION) }} />
    </main>
  );
}
