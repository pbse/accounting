// Each public page's title and description: the build writes them into the page's static HTML for search engines and
// link previews (prerender.mjs), and the layout sets them as a visitor moves between pages.
export type PageMeta = { title: string; description: string };

const PAGES: Record<string, PageMeta> = {
  "/": {
    title: "Krith · The work, prepared. AI agents for the month-end close",
    description: "Krith’s AI agents prepare the work behind every engagement for accounting firms, starting with the month-end close. Your people review, decide and sign.",
  },
  "/about": {
    title: "About Krith · Agents prepare the close, people sign",
    description: "Krith means done. Its AI agents take on the preparation behind a month-end close, and every decision stays with the accounting firm that signs.",
  },
  "/faq": {
    title: "Krith FAQ · AI close preparation for accounting firms",
    description: "What accounting firms ask about Krith: what its AI agents prepare, who approves the work, where client data is stored, and how to start.",
  },
  "/security": {
    title: "Security · How Krith protects client data",
    description: "How Krith keeps client data, and the authority over it, where it belongs: people decide, every figure keeps its source, each firm is walled off.",
  },
  "/privacy": {
    title: "Privacy notice · Krith",
    description: "How Krith handles information on its website and in the Krith workspace: what is collected, who sees it, where it is stored and your choices.",
  },
  "/contact": {
    title: "Talk to us · See Krith prepare one of your closes",
    description: "Tell us how your accounting firm works and where month-end takes its time. A person reads every note and replies, then shows you the work, prepared.",
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
