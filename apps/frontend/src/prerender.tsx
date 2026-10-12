import { renderToStaticMarkup } from "react-dom/server";
import { Routes, StaticRouter } from "react-router";
import { marketingRoutes } from "./features/marketing/routes";

export { pageMeta } from "./features/marketing/meta";

// The public pages a visitor or a crawler can land on, each written to static HTML at build time (prerender.mjs) so it
// has its words before its script runs; in the browser the app then renders the same page over it. 404.html is what
// the assets serve, with a 404 status, for any path that is neither a file nor a page. Not /contact: its form sends
// only from the app, and a static copy would submit the visitor's details into the page's own URL.
export const pages: Record<string, string> = { "/": "static-home.html", "/security": "security.html", "/privacy": "privacy.html", "/about": "about.html", "/faq": "faq.html", "/404": "404.html" };

// The other paths the browser app opens directly get the empty page under their own names, so they are found too.
export const shells: Record<string, string> = { "/contact": "contact.html", "/login": "login.html", "/request": "request.html" };

export function render(path: string) {
  return renderToStaticMarkup(
    <StaticRouter location={path}>
      <Routes>{marketingRoutes()}</Routes>
    </StaticRouter>,
  );
}
