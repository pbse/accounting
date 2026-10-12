// Each public page's title and description: the build writes them into the page's static HTML for search engines and
// link previews (prerender.mjs), and the layout sets them as a visitor moves between pages.
export type PageMeta = { title: string; description: string };

const PAGES: Record<string, PageMeta> = {
  "/": {
    title: "Krith · The work, prepared.",
    description: "Krith prepares the work behind every engagement, starting with the month-end close. Your people review, decide and sign.",
  },
  "/about": {
    title: "About · Krith",
    description: "Krith means done. A team of agents takes on the preparation behind a close; every decision stays with the firm that signs.",
  },
  "/faq": {
    title: "Questions · Krith",
    description: "What accounting firms ask before they hand Krith a month: the product, working with the agents, their data, and how to start.",
  },
  "/security": {
    title: "Security · Krith",
    description: "How Krith keeps client data, and the authority over it, where it belongs: people decide, every figure keeps its source, each firm is walled off.",
  },
  "/privacy": {
    title: "Privacy notice · Krith",
    description: "How Krith handles information on this website and in the Krith workspace.",
  },
  "/contact": {
    title: "Talk to us · Krith",
    description: "Tell us how your firm works and where month-end takes its time. A person reads every note and replies.",
  },
  "/login": {
    title: "Log in · Krith",
    description: "Sign in to the Krith workspace with your company account.",
  },
};

export const notFoundMeta: PageMeta = { title: "Page not found · Krith", description: "This page doesn’t exist." };

export function pageMeta(path: string): PageMeta {
  return PAGES[path] ?? notFoundMeta;
}
