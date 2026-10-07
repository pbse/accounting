// Who is behind Krith, kept to a few facts: when and where it was founded, and its founder. The same facts are on the
// page as structured data, so search engines and anyone verifying the company can read them (docs/marketing-site.md).
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

export function About() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Company</p>
        <h1>About Krith</h1>
        <p className="k-lede">Krith prepares the work behind every engagement, starting with the month-end close. Your people review, decide and sign.</p>
      </div>
      <article className="k-doc">
        <section>
          <h2>Headquarters</h2>
          <p>New York City. Krith was founded there in 2026.</p>
        </section>
        <section>
          <h2>Leadership</h2>
          <p>
            <a href={FOUNDER_SITE}>Prashant Bansal</a>, Founder and CEO
          </p>
        </section>
      </article>
      <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify(ORGANIZATION) }} />
    </main>
  );
}
