import { MAILBOX } from "./Contact";

// schema.org data for search and answer engines (docs/marketing-site.md): the company, which Home and About carry, the
// site on Home and the questions on the FAQ. It states only what the pages say.
const SITE = "https://getkrith.com";

export const organization = {
  "@type": "Organization",
  "@id": `${SITE}/#organization`,
  name: "Krith",
  url: SITE,
  logo: `${SITE}/logo.png`,
  description: "Krith is a team of AI agents that prepares accounting work for firms, starting with the month-end close. People review, decide and sign.",
  foundingDate: "2026",
  foundingLocation: { "@type": "Place", name: "New York, NY" },
  address: { "@type": "PostalAddress", addressLocality: "New York", addressRegion: "NY", addressCountry: "US" },
  contactPoint: { "@type": "ContactPoint", contactType: "sales", url: `${SITE}/contact`, email: MAILBOX, availableLanguage: "en" },
};

export const website = {
  "@type": "WebSite",
  "@id": `${SITE}/#website`,
  url: `${SITE}/`,
  name: "Krith",
  inLanguage: "en",
  publisher: { "@id": `${SITE}/#organization` },
};

// The data as a script the page carries; "<" is escaped so no value can end the script early.
export function JsonLd({ data }: { data: object }) {
  return <script type="application/ld+json" dangerouslySetInnerHTML={{ __html: JSON.stringify({ "@context": "https://schema.org", ...data }).replace(/</g, "\\u003c") }} />;
}
