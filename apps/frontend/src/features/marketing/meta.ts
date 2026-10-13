// Each public page's title and description: the build writes them into the page's static HTML for search engines and
// link previews (prerender.mjs), and the layout sets them as a visitor moves between pages.
export type PageMeta = { title: string; description: string };

const PAGES: Record<string, PageMeta> = {
  "/": {
    title: "Krith · The work, prepared.",
    description: "Krith’s AI agents prepare the work behind every engagement for accounting firms and finance teams. Your people review, decide and sign.",
  },
  "/about": {
    title: "About Krith · Agents prepare, people sign",
    description: "Krith means done. Its AI agents take on the preparation behind an accounting firm’s work, and every decision stays with the people who sign.",
  },
  "/faq": {
    title: "Krith FAQ · AI agents for accounting firms",
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
    title: "Talk to us · Krith",
    description: "Tell us how your accounting firm works and where its hours go. A person reads every note and replies, then shows you the work, prepared.",
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
