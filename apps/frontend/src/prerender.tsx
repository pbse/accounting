import { renderToStaticMarkup } from "react-dom/server";
import { Routes, StaticRouter } from "react-router";
import { marketingRoutes } from "./features/marketing/routes";

export { pageMeta } from "./features/marketing/meta";

// The public pages a visitor or a crawler can land on, each written to static HTML at build time (prerender.mjs) so it
// has its words before its script runs; in the browser the app then renders the same page over it. 404.html is what
// the assets serve, with a 404 status, for any path that is neither a file nor a page. /contact's form stays off until the
// app runs (Contact.tsx), so its static copy cannot send anything.
export const pages: Record<string, string> = { "/": "static-home.html", "/security": "security.html", "/privacy": "privacy.html", "/about": "about.html", "/faq": "faq.html", "/contact": "contact.html", "/404": "404.html" };

// The other paths the browser app opens directly get the empty page under their own names, so they are found too.
export const shells: Record<string, string> = { "/login": "login.html", "/request": "request.html" };

export function render(path: string) {
  return renderToStaticMarkup(
    <StaticRouter location={path}>
      <Routes>{marketingRoutes()}</Routes>
    </StaticRouter>,
  );
}
