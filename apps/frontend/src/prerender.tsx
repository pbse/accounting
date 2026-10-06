import { renderToStaticMarkup } from "react-dom/server";
import { Routes, StaticRouter } from "react-router";
import { marketingRoutes } from "./features/marketing/routes";

// The public pages a visitor or a crawler can land on, each written to static HTML at build time (prerender.mjs) so it
// has its words before its script runs; in the browser the app then renders the same page over it.
export const pages: Record<string, string> = { "/": "static-home.html", "/security": "security.html", "/contact": "contact.html", "/privacy": "privacy.html" };

export function render(path: string) {
  return renderToStaticMarkup(
    <StaticRouter location={path}>
      <Routes>{marketingRoutes()}</Routes>
    </StaticRouter>,
  );
}
