import type { ReactNode } from "react";
import { Route } from "react-router";
import { MarketingLayout } from "./Layout";
import { Home } from "./Home";
import { Contact } from "./Contact";
import { Security } from "./Security";
import { Privacy } from "./Privacy";
import { About } from "./About";
import { Faq } from "./Faq";
import { NotFound } from "./NotFound";

// The public site's pages, as the app routes them and as the build renders them to static HTML (src/prerender.tsx).
export function marketingRoutes(more?: ReactNode) {
  return (
    <Route element={<MarketingLayout />}>
      <Route index element={<Home />} />
      <Route path="contact" element={<Contact />} />
      <Route path="security" element={<Security />} />
      <Route path="privacy" element={<Privacy />} />
      <Route path="about" element={<About />} />
      <Route path="faq" element={<Faq />} />
      {more}
      <Route path="*" element={<NotFound />} />
    </Route>
  );
}
